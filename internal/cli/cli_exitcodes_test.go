package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"mindloop/internal/runner"
	"mindloop/internal/traj"
)

// 本文件钉死 README「开发」节承诺的退出码语义：
// 0 成功；1 运行失败；2 用法错误；3 run 的失速/轮次耗尽。
// cli_test.go 已覆盖 0（裸调用）与 2（未知命令 "taj"），
// 这里补齐 1、3 以及更细的 2 类路径。
//
// 全部用例走 Execute(ctx, args, &stdout, &stderr) 注入，不经子进程
// （root.go 的进程级约定）；隔离的 MINDLOOP_HOME 由 newIsoHome 提供。

// newIsoHome 建立测试专用的状态根目录（与 cli_test.go 的 newTestHome
// 同义，独立命名以免与既有测试辅助耦合）。
func newIsoHome(t *testing.T) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
}

// isoRun 在隔离 HOME 下执行一次 CLI 调用，返回退出码与两路输出。
func isoRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Execute(context.Background(), args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// TestExitCodeOneAndTwoPaths：退出码 1（运行失败）与 2（用法错误）
// 的表驱动覆盖。运行失败用例都在触碰模型/沙箱之前就失败（traj 层
// 的 Load/FindStep），因此不需要真实 bash 或 API key。
func TestExitCodeOneAndTwoPaths(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantCode  int
		stderrHas string // 非空时断言 stderr 包含该串
		stdoutHas string // 非空时断言 stdout 包含该串
	}{
		// —— 退出码 2：用法错误 ——
		{
			name:      "未知命令给出建议（mnd→mind）",
			args:      []string{"mnd"},
			wantCode:  2,
			stderrHas: "mind",
		},
		{
			name:      "未知旗标走 cobra 解析报错",
			args:      []string{"traj", "tail", "--bogus-flag", "x"},
			wantCode:  2,
			stderrHas: "bogus-flag",
		},
		{
			name:      "traj new 拒绝多余位置参数",
			args:      []string{"traj", "new", "surplus"},
			wantCode:  2,
			stderrHas: "",
		},
		{
			name:      "run 参数不足回中文用法提示",
			args:      []string{"run", "only-one-arg"},
			wantCode:  2,
			stderrHas: "用法: mindloop run",
		},
		// —— 退出码 1：运行失败 ——
		{
			name:      "run 轨迹不存在（先于模型/沙箱失败）",
			args:      []string{"run", "00000000", "任务"},
			wantCode:  1,
			stderrHas: "",
		},
		{
			name:      "traj show 前缀未命中",
			args:      []string{"traj", "show", "ffffffff"},
			wantCode:  1,
			stderrHas: "",
		},
		{
			name:     "traj new --parent 未命中",
			args:     []string{"traj", "new", "--parent", "zzzzzzzz"},
			wantCode: 1,
		},
		// —— 退出码 0：成功冒烟 ——
		{
			name:      "version 子命令",
			args:      []string{"version"},
			wantCode:  0,
			stdoutHas: "mindloop v",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newIsoHome(t)
			code, out, errOut := isoRun(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit = %d，应为 %d（stderr: %s）", code, tc.wantCode, errOut)
			}
			if tc.stderrHas != "" && !strings.Contains(errOut, tc.stderrHas) {
				t.Fatalf("stderr 缺少 %q: %q", tc.stderrHas, errOut)
			}
			if tc.stdoutHas != "" && !strings.Contains(out, tc.stdoutHas) {
				t.Fatalf("stdout 缺少 %q: %q", tc.stdoutHas, out)
			}
		})
	}
}

// TestExitCodeOneViaAppendBadField：traj append 的 --field 值不是
// K=V 形式 → 运行失败（退出码 1）。先建真实轨迹再触发，覆盖
// DisableFlagParsing + collectFieldFlags 这条手工解析路径的失败面。
func TestExitCodeOneViaAppendBadField(t *testing.T) {
	newIsoHome(t)
	code, out, errOut := isoRun(t, "traj", "new", "--slug", "fx")
	if code != 0 {
		t.Fatalf("traj new: %d %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	code, _, errOut = isoRun(t, "traj", "append", id, "facts", "--field", "broken")
	if code != 1 {
		t.Fatalf("append 坏 --field exit = %d，应为 1（stderr: %s）", code, errOut)
	}
}

// TestExitCodeThreeMapping：退出码 3 的端到端路径（ErrStalled /
// ErrMaxIterations）必须由 runner 真实执行脚本才能到达，而 cli 层
// 测试约定不经子进程——因此这里直接单测退出码映射函数
// runFinishedWithOutcome：运行的真实结局包装为 exitError{code:3}，
// 诊断（轨迹路径、工作目录）写 stderr 而非 stdout。
func TestExitCodeThreeMapping(t *testing.T) {
	newIsoHome(t)
	tl, err := traj.Create(context.Background(), "exit3")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var out, errBuf bytes.Buffer
	c := &CLI{ctx: context.Background(), stdout: &out, stderr: &errBuf}

	err = c.runFinishedWithOutcome(runner.Result{WorkDir: tl.Dir}, tl, runner.ErrStalled)
	var ee exitError
	if !errors.As(err, &ee) {
		t.Fatalf("应返回 exitError，得到 %T: %v", err, err)
	}
	if ee.code != 3 {
		t.Fatalf("退出码 = %d，应为 3（失速）", ee.code)
	}
	// 诊断必须带运行记录位置与保留的工作目录。
	if !strings.Contains(errBuf.String(), tl.Path) {
		t.Fatalf("stderr 应含轨迹路径 %q: %q", tl.Path, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "工作目录") {
		t.Fatalf("stderr 应提示保留的工作目录: %q", errBuf.String())
	}
	if out.String() != "" {
		t.Fatalf("退出码 3 是运行的真实结局，最终答案不应进 stdout: %q", out.String())
	}

	// 轮次耗尽走同一出口。
	errBuf.Reset()
	err = c.runFinishedWithOutcome(runner.Result{}, tl, runner.ErrMaxIterations)
	if !errors.As(err, &ee) || ee.code != 3 {
		t.Fatalf("ErrMaxIterations 应映射为退出码 3，得到 %v", err)
	}
}
