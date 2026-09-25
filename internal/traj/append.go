package traj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"mindloop/internal/ids"
)

// ErrLockTimeout：在超时上限内未能取得目录锁。用 errors.Is 判断；
// 错误文本里附带 probeLock 的占用者信息以便定位。
var ErrLockTimeout = errors.New("mindloop: 等待锁超时")

// Append 在轨迹的目录锁保护下写入一个步骤。整行用一次 O_APPEND
// Write 写出——在 POSIX 和 NTFS（FILE_APPEND_DATA）上都是单次调用
// 级原子——因此即使在病态的锁竞争下，读取方看到的也是完整的行，
// 绝不会是交错的字节。锁的真正职责是给多步骤操作定序，以及将来
// 的 blob 分配，而不是字节完整性。
//
// 等待锁受两个信号约束：Timeline.LockTimeout（或默认值）与 ctx
// 取消——心智循环的停机靠后者传播。
func (t *Timeline) Append(ctx context.Context, step Step) error {
	release, err := acquireDirLock(ctx, t.Path+".lock", t.lockTimeout())
	if err != nil {
		return err
	}
	defer release()
	return t.writeStep(step)
}

// writeStep 不加锁直接追加：用于刚创建、仍属私有的轨迹（新文件
// 没有并发写者——与 Headlong 对 traj new 的豁免相同），以及已经
// 持有锁的调用方。
func (t *Timeline) writeStep(step Step) error {
	line, err := json.Marshal(step)
	if err != nil {
		return fmt.Errorf("mindloop: 序列化步骤: %w", err)
	}
	line = append(line, '\n')
	f, err := os.OpenFile(t.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("mindloop: 打开 %s: %w", t.Path, err)
	}
	defer f.Close()
	n, err := f.Write(line)
	if err != nil {
		return fmt.Errorf("mindloop: 追加 %s: %w", t.Path, err)
	}
	if n != len(line) {
		return fmt.Errorf("mindloop: 追加 %s: 短写（%d/%d 字节）", t.Path, n, len(line))
	}
	return nil
}

// LastStep 返回文件中最后一个可解析的步骤。
func (t *Timeline) LastStep() (Step, error) {
	steps, err := t.Steps()
	if err != nil {
		return Step{}, err
	}
	if len(steps) == 0 {
		return Step{}, fmt.Errorf("mindloop: %s 是空的", t.ID)
	}
	return steps[len(steps)-1], nil
}

// Merge 把子轨迹的结果记到本（父）轨迹上：一个指向子轨迹最后一步
// 的 merge 步骤，content 即子轨迹的答案——Headlong fork/merge 中
// 写回的那一半。
func (t *Timeline) Merge(ctx context.Context, child *Timeline, content string) (Step, error) {
	last, err := child.LastStep()
	if err != nil {
		return Step{}, err
	}
	ref, err := rel(t.Dir, child.Dir)
	if err != nil {
		return Step{}, err
	}
	m := Step{Type: TypeMerge, StepID: ids.NewUUID(), TS: NowString(), Fields: map[string]any{
		"from_traj":     child.ID,
		"from_step":     last.StepID,
		"from_traj_ref": ref,
		"content":       content,
	}}
	return m, t.Append(ctx, m)
}
