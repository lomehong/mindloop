package traj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/ids"
)

const (
	lockPoll = 10 * time.Millisecond
	// lockGrace 覆盖 Mkdir 与写属主文件之间崩溃的窗口：年轻且无属主
	// 的锁先不动它。
	lockGrace = 2 * time.Second
	ownerFile = "owner.json"
)

type lockOwner struct {
	PID     int    `json:"pid"`
	Created string `json:"created"`
	Exe     string `json:"exe"`
}

// AcquireDirLock 是目录锁的公开入口——其他包（如 mem 的写序号）
// 需要同一套跨进程互斥语义时复用，而不是各写一份。
func AcquireDirLock(ctx context.Context, dir string, timeout time.Duration) (func() error, error) {
	return acquireDirLock(ctx, dir, timeout)
}

// TryDirLock 尝试获取目录锁，绝不等待。锁空闲或属主已死（偷取）
// 时返回 owned=true；已被活着的属主持有时返回 owned=false——调用方
// 据此判断"服务是否已在运行"（调度器的单实例语义）。
func TryDirLock(ctx context.Context, dir string) (release func() error, owned bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	// 锁目录的父目录（如 <轨迹>/run）未必存在。
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, false, fmt.Errorf("mindloop: 锁 %s: %w", dir, err)
	}
	merr := os.Mkdir(dir, 0o755)
	if merr == nil {
		return claimDir(dir)
	}
	if !errors.Is(merr, os.ErrExist) && !errors.Is(merr, os.ErrPermission) {
		return nil, false, fmt.Errorf("mindloop: 锁 %s: %w", dir, merr)
	}
	if rel, ok := stealLock(dir); ok {
		return rel, true, nil
	}
	return nil, false, nil
}

// ProcessAlive 报告进程是否存活（锁属主诊断用）。
func ProcessAlive(pid int) bool { return processAlive(pid) }

// LockOwnerAlive 报告 dir 处目录锁的属主是否存活。锁目录存在但
// 属主已死是残留锁——监控面据此判断"心智是否真的在跑"，而不是
// 只看锁目录存在（那是把崩溃现场当成运行中）。
func LockOwnerAlive(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		return false
	}
	var o lockOwner
	if json.Unmarshal(data, &o) != nil || o.PID <= 0 {
		return false
	}
	return processAlive(o.PID)
}

// acquireDirLock 获取 dir 处的咨询锁：一次原子的 mkdir，外加一份
// 记录持有者的 owner 文件。mkdir 在 Windows 状态根目录可能落在的
// 每种文件系统上（含 NTFS）都是原子的——这正是 Headlong 用它而
// 不用 flock 的原因，而且跨进程不需要传递句柄。
//
// 与 Headlong 按年龄偷锁的做法不同（被挂起的写者可能被抢走锁），
// 这里的锁只在两种情况下被偷走：记录的属主进程被确认死亡，或锁
// 无属主且已超过 lockGrace（崩溃窗口）。
//
// 等待受 timeout 与 ctx 双重约束；超时返回包装过的 ErrLockTimeout，
// 取消返回 ctx.Err()。
func acquireDirLock(ctx context.Context, dir string, timeout time.Duration) (func() error, error) {
	deadline := time.Now().Add(timeout)
	absentDenied := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			rel, _, cerr := claimDir(dir)
			if cerr != nil {
				return nil, cerr
			}
			return rel, nil
		}
		// Windows 细节：目录处于删除中（delete-pending）时，同名
		// mkdir 返回的是 ACCESS_DENIED 而不是 AlreadyExists——它
		// 同样是暂时竞争，要重试而不是报错。但若锁目录根本不存在
		// 而拒绝仍连续发生，那是父目录不可写（Headlong 曾有桥接
		// 用户在只读目录上转满了整个超时），快速失败。
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission) {
			if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
				absentDenied++
				if absentDenied > 5 {
					return nil, fmt.Errorf("mindloop: 锁 %s: mkdir 被拒绝且锁目录不存在（父目录不可写？）: %w", dir, err)
				}
			} else {
				absentDenied = 0
			}
		} else {
			return nil, fmt.Errorf("mindloop: 锁 %s: %w", dir, err)
		}
		if rel, ok := stealLock(dir); ok {
			return rel, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: 锁 %s（%s）", ErrLockTimeout, dir, probeLock(dir))
		}
		time.Sleep(lockPoll)
	}
}

// lockView 是锁目录在某一瞬间的快照——"能不能偷"与"残躯校验"
// 都以同一份观察为准。偷锁互斥的闭合靠 stealLock 的 rename CAS
// （rename 原子，仅一个偷取者能移走原目录）；快照比对负责识别
// "移走的已不是当初观测的死锁"的极端交错。
type lockView struct {
	hasOwner bool
	data     []byte
	dirMod   time.Time
}

// observeLock 拍下锁目录快照；目录不存在返回 false。
func observeLock(dir string) (lockView, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	fi, statErr := os.Stat(dir)
	if statErr != nil {
		return lockView{}, false
	}
	return lockView{hasOwner: err == nil, data: data, dirMod: fi.ModTime()}, true
}

// stealable 依据快照判断锁是否可偷：属主确认死亡 ⇒ 偷；无属主或
// 属主文件不可解析 ⇒ 只有目录足够老（写者死于声明中途的崩溃窗口
// 之外）才偷。
func (v lockView) stealable() bool {
	if !v.hasOwner {
		return v.staleEnough()
	}
	var o lockOwner
	if json.Unmarshal(v.data, &o) != nil || o.PID <= 0 {
		return v.staleEnough()
	}
	return !processAlive(o.PID)
}

func (v lockView) staleEnough() bool {
	return time.Since(v.dirMod) > lockGrace
}

// sameAs 报告两次观察之间锁目录是否原封未动——owner 文件一个字节
// 没变、目录 mtime 也没前进。变了就意味着有人声明或重新声明过。
func (v lockView) sameAs(other lockView) bool {
	return v.hasOwner == other.hasOwner &&
		bytes.Equal(v.data, other.data) &&
		v.dirMod.Equal(other.dirMod)
}

// stealLock 以 rename CAS 完成"校验 → 原子移走 → 校验残躯 → 原地
// 声明"的偷锁序列。破坏性动作不是 RemoveAll 而是 rename：rename 在
// 内核级原子，两个并发偷取者只有一个能把原路径的目录成功移走——
// 输家要么在 rename 处直接失败，要么稍后观察到的是赢家的活属主
// （stealable=false）。"双双成功"从此在结构上不可能：此前的两次
// 快照比对把 TOCTOU 收窄到了调度间隙的微秒级但没有闭合（P2 两次
// 观测都在 P1 claim 之前、RemoveAll 落在其后时仍会删掉活锁——
// lead 全量门禁抓到过一次），rename CAS 把它关死。
//
// 任何失败一律放弃本轮偷取：Windows 上目录内有打开句柄（外部读者
// /AV）时 rename 报 ACCESS_DENIED/sharing violation，POSIX 上父目录
// 只读时报 EACCES——保守方向正确，等待方重试或吃超时，绝不误伤
// 可能活着的锁。
func stealLock(dir string) (release func() error, ok bool) {
	view, found := observeLock(dir)
	if !found || !view.stealable() {
		return nil, false
	}
	// CAS：把原路径原子移走。失败 ⇒ 已被并发方移走/目录被外部打开
	// /已消失——放弃，不动任何人的锁。
	scratch := fmt.Sprintf("%s.stealing-%d-%s", dir, os.Getpid(), ids.Short(ids.NewUUID(), 8))
	if err := os.Rename(dir, scratch); err != nil {
		return nil, false
	}
	// 移走的残躯必须仍是当初观测到的那把死锁：极端交错下它可能是
	// 别人在我们观测与 rename 之间换上的新活锁——改名还原并放弃，
	// 绝不删活锁。还原失败（微秒窗内原路径又被占）则留下残躯目录：
	// 宁可留无害垃圾，也不冒删活锁的险。
	if fresh, found := observeLock(scratch); !found || !view.sameAs(fresh) {
		_ = os.Rename(scratch, dir)
		return nil, false
	}
	// 死锁已被安全隔离：在原路径声明。
	if os.Mkdir(dir, 0o755) != nil {
		// 原路径被并发方抢先重占：残躯已验证是死锁，可删。
		_ = os.RemoveAll(scratch)
		return nil, false
	}
	rel, owned, err := claimDir(dir)
	if err != nil || !owned {
		_ = os.RemoveAll(scratch)
		return nil, false
	}
	_ = os.RemoveAll(scratch) // 清理已验证的死锁残躯；失败只是无害垃圾
	return rel, true
}

// probeLock 描述锁的当前占用者，用于超时报错：运维拿到错误就能
// 直接定位持有进程，而不是只看到一句"被占用"。
func probeLock(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		entries, _ := os.ReadDir(dir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) == 0 {
			return "目录存在但为空且无属主文件"
		}
		return "无属主文件，目录内容: " + strings.Join(names, ",")
	}
	var o lockOwner
	if err := json.Unmarshal(data, &o); err != nil {
		return "属主文件不可解析: " + string(data)
	}
	state := "存活"
	if !processAlive(o.PID) {
		state = "已死"
	}
	return fmt.Sprintf("持有者 pid=%d (%s) 声明于 %s", o.PID, state, o.Created)
}

// claimDir 在锁目录归我们之后写属主文件并自检。此刻锁目录被我们
// 独占，直接写入即可——读者要么读到完整的属主文件，要么读到半行
// （解析失败后走宽限路径，同样保守）。绝不经过共享的临时文件：并
// 发声明会互相踩踏，把彼此的锁整个删掉。
// 写完读回校验：目录里的 owner.json 必须逐字节等于我们刚写的内容，
// 否则说明声明被并发者覆盖（病理场景，宽限期本应挡住它）——交还
// 锁并报错，绝不占着一把已被别人改写的锁。
func claimDir(dir string) (release func() error, owned bool, err error) {
	rel, claimed, werr := writeClaim(dir)
	if werr != nil {
		return nil, false, werr
	}
	data, rerr := os.ReadFile(filepath.Join(dir, ownerFile))
	if rerr != nil || !bytes.Equal(data, claimed) {
		_ = rel()
		return nil, false, fmt.Errorf("mindloop: 锁 %s: 属主文件写后即被覆盖", dir)
	}
	return rel, true, nil
}

func writeClaim(dir string) (release func() error, claimed []byte, err error) {
	exe, _ := os.Executable()
	o := lockOwner{PID: os.Getpid(), Created: NowString(), Exe: filepath.Base(exe)}
	claimed, err = json.Marshal(o)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("mindloop: 序列化锁属主: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ownerFile), claimed, 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("mindloop: 锁属主 %s: %w", dir, err)
	}
	released := false
	release = func() error {
		if released {
			return nil
		}
		released = true
		// Windows: 只要还有并发读者握着 owner.json 的句柄（读取只
		// 有微秒级），目录就处于 delete-pending，RemoveAll 会失败。
		// 短暂重试直到句柄全部关闭——静默放弃的代价是锁永久泄漏，
		// 所有后来者吃满超时，值得多花几毫秒。
		var err error
		for i := 0; i < 200; i++ {
			if err = os.RemoveAll(dir); err == nil {
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		return err
	}
	return release, claimed, nil
}
