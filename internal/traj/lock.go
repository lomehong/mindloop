package traj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		rel, err := claimLock(dir)
		return rel, err == nil, err
	}
	if !errors.Is(merr, os.ErrExist) && !errors.Is(merr, os.ErrPermission) {
		return nil, false, fmt.Errorf("mindloop: 锁 %s: %w", dir, merr)
	}
	if canStealLock(dir) {
		if rmErr := os.RemoveAll(dir); rmErr == nil {
			if os.Mkdir(dir, 0o755) == nil {
				rel, err := claimLock(dir)
				return rel, err == nil, err
			}
		}
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
			return claimLock(dir)
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
		if canStealLock(dir) {
			if rmErr := os.RemoveAll(dir); rmErr == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: 锁 %s（%s）", ErrLockTimeout, dir, probeLock(dir))
		}
		time.Sleep(lockPoll)
	}
}

func canStealLock(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		return staleEnough(dir)
	}
	var o lockOwner
	if json.Unmarshal(data, &o) != nil || o.PID <= 0 {
		return staleEnough(dir)
	}
	return !processAlive(o.PID)
}

func staleEnough(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && time.Since(fi.ModTime()) > lockGrace
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

// claimLock 在锁目录归我们之后写属主文件。此刻锁目录被我们独占，
// 直接写入即可——读者要么读到完整的属主文件，要么读到半行（解析
// 失败后走宽限路径，同样保守）。绝不经过共享的临时文件：并发声明
// 会互相踩踏，把彼此的锁整个删掉。
func claimLock(dir string) (func() error, error) {
	exe, _ := os.Executable()
	o := lockOwner{PID: os.Getpid(), Created: NowString(), Exe: filepath.Base(exe)}
	data, err := json.Marshal(o)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("mindloop: 序列化锁属主: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ownerFile), data, 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("mindloop: 锁属主 %s: %w", dir, err)
	}
	released := false
	return func() error {
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
	}, nil
}
