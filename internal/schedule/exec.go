package schedule

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"mindloop/internal/sandbox"
)

// executeSandbox 是 exec 条目的默认执行器：经沙箱（bash + Windows
// Job Object 整树管辖）在身份目录里执行命令。环境是 childenv 白名单
// + 显式业务变量——父环境的模型凭据不下传；超时杀整棵树，不留残
// 余子进程。
func (r *Runtime) executeSandbox(ctx context.Context, p Parsed) ExecOutcome {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	res, err := sandbox.Run(ctx, sandbox.Request{
		Script:  p.Item.Exec,
		Dir:     r.opts.Dir,
		Env:     r.opts.Env,
		Timeout: timeout,
		// 安静不是死：无输出的长脚本只受总时限约束。
		IdleTimeout: timeout,
	})
	if err != nil {
		return ExecOutcome{Err: err, Detail: err.Error()}
	}
	out := ExecOutcome{ExitCode: res.ExitCode, Duration: res.Duration}
	if res.KillReason != "" {
		out.Err = fmt.Errorf("被终止（%s）", res.KillReason)
	} else if res.ExitCode != 0 {
		out.Err = fmt.Errorf("退出码 %d", res.ExitCode)
	}
	if out.Err != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		out.Detail = oneLine(detail, 2000)
	}
	return out
}

// ExecuteNow 手动执行一个 exec 条目（CLI schedule run）：同步执行、
// 结果原样返回；只落一行日志，不动调度状态投影——手动触发不是
// 日程节拍的一部分。
func (r *Runtime) ExecuteNow(ctx context.Context, p Parsed) ExecOutcome {
	out := r.opts.Execute(ctx, p)
	if out.Err != nil {
		suffix := ""
		if out.Detail != "" {
			suffix = "：" + out.Detail
		}
		r.logf("[%s] 手动执行失败: %v%s", p.Item.ID, out.Err, suffix)
	} else {
		r.log("[%s] 手动执行完成（exit %d，%s）", p.Item.ID, out.ExitCode, out.Duration.Round(time.Millisecond))
	}
	return out
}

// log 追加一行执行日志（只落文件——成功路径保持安静）。
func (r *Runtime) log(format string, args ...any) {
	r.appendLog(time.Now(), format, args...)
}

// logf 记一条重要事件：文件 + Logger（失败、警告要让人看见）。
func (r *Runtime) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if r.opts.Logger != nil {
		r.opts.Logger("日程: %s", msg)
	}
	r.appendLog(time.Now(), "%s", msg)
}

// appendLog 追加一行执行日志（超限先轮转为 .1）。写日志绝不反过来
// 影响调度：任何失败都只是这一行没落上。
func (r *Runtime) appendLog(now time.Time, format string, args ...any) {
	if r.opts.LogPath == "" {
		return
	}
	line := now.Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, args...) + "\n"
	if fi, err := os.Stat(r.opts.LogPath); err == nil && fi.Size() > maxLogBytes {
		_ = os.Rename(r.opts.LogPath, r.opts.LogPath+".1")
	}
	f, err := os.OpenFile(r.opts.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// oneLine 折叠空白并截断到 limit 个字符（rune 安全）。
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
