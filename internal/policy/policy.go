// Package policy 是生成脚本的执行授权策略——ask / trusted / deny
// 三档，以及 ask 模式下的审批等待（文件控制面）。它是
// runner.BeforeExecute 的统一实现：CLI run、显式任务与自主行动共用
// 同一控制点，等待期间不调用模型。
//
// 边界说明：本包只决定"这一个脚本此刻能不能执行"，不做沙箱隔离
// ——trusted 是启动授权约定，不是操作系统访问隔离；脚本仍以当前
// 用户权限运行，可能访问本用户可访问的文件与网络。
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mindloop/internal/runner"
)

// Mode 是执行策略。
type Mode string

const (
	// Ask：缺省策略。每个脚本执行前展示正文、工作目录与运行归属，
	// 等待明确批准。
	Ask Mode = "ask"
	// Trusted：用户显式选择。直接执行——宿主机权限风险由用户承担。
	Trusted Mode = "trusted"
	// Deny：禁止脚本执行（模型仍可思考，但不产生副作用）。
	Deny Mode = "deny"
)

// 哨兵错误。
var (
	// ErrDenied：脚本未获批准（策略禁止或用户拒绝），未执行。
	ErrDenied = errors.New("policy: 脚本执行被拒绝")
	// ErrTimeout：审批等待超时（视同未批准），脚本未执行。
	ErrTimeout = errors.New("policy: 审批超时（未获批准，脚本未执行）")
)

// ParseMode 解析策略文本：空与未知值都落到最保守的 ask，绝不
// 因配置拼写错误静默放行。
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trusted":
		return Trusted
	case "deny":
		return Deny
	default:
		return Ask
	}
}

// ModeFromEnv 读取 MINDLOOP_EXEC_POLICY（未设置即 ask）。
func ModeFromEnv() Mode { return ParseMode(os.Getenv("MINDLOOP_EXEC_POLICY")) }

// Dir 返回一条轨迹的审批控制面目录（沿用 mind 控制面的 run/ 位置）。
func Dir(tlDir string) string { return filepath.Join(tlDir, "run", "approvals") }

// ScriptHash 是脚本内容的 sha256 十六进制——审批对象的身份。
// 脚本任何变化都改变哈希，批准的复用因此天然收窄到同一份脚本。
func ScriptHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return hex.EncodeToString(sum[:])
}

// Gate 实现 runner.BeforeExecute：策略判定 + ask 模式的文件控制面
// 等待。批准缓存只存在于实例内（进程生命周期）且带有效期——重启、
// 超时、换脚本、换工作目录、换运行都会让旧批准失效。
type Gate struct {
	dir  string
	mode Mode

	// PollInterval 是等待决定的轮询间隔（默认 150ms）。
	PollInterval time.Duration
	// ApprovalTTL 是审批有效期：等待上限与批准缓存寿命同源
	// （默认 10 分钟）。
	ApprovalTTL time.Duration
	// Logger 输出等待提示（人看的信息，进 stderr）。
	Logger func(format string, args ...any)
	// WaitHook 在进入/离开审批等待时各回调一次；离开回调覆盖批准、
	// 拒绝、超时、取消全部出口——任务面用它同步 awaiting_approval
	// 状态，不区分结局。
	WaitHook func(ex runner.Execution, waiting bool)

	now   func() time.Time
	cache map[string]time.Time // hash|workdir|runID → 批准到期时刻
}

// NewGate 构造一个门。dir 传 policy.Dir(轨迹目录)；mode 传解析后的
// 策略（缺省 ask）。
func NewGate(dir string, mode Mode) *Gate {
	return &Gate{dir: dir, mode: mode, now: time.Now, cache: map[string]time.Time{}}
}

// Mode 返回门的当前策略。
func (g *Gate) Mode() Mode { return g.mode }

// Authorize 是统一授权入口：trusted 直接放行；deny 直接拒绝；ask
// 在批准缓存命中且未过期时放行，否则进入等待。
func (g *Gate) Authorize(ctx context.Context, ex runner.Execution) error {
	switch g.mode {
	case Trusted:
		return nil
	case Deny:
		return fmt.Errorf("%w（执行策略 deny：禁止一切脚本执行）", ErrDenied)
	}
	hash := ScriptHash(ex.Script)
	key := approvalKey(hash, ex)
	if exp, ok := g.cache[key]; ok {
		if g.now().Before(exp) {
			return nil
		}
		delete(g.cache, key)
	}
	return g.wait(ctx, hash, ex)
}

// approvalKey 是批准缓存的键：脚本 + 工作目录 + 运行——三者任一
// 变化都必须重新批准。
func approvalKey(hash string, ex runner.Execution) string {
	return hash + "|" + ex.WorkDir + "|" + ex.RunID
}

func (g *Gate) poll() time.Duration {
	if g.PollInterval > 0 {
		return g.PollInterval
	}
	return 150 * time.Millisecond
}

func (g *Gate) ttl() time.Duration {
	if g.ApprovalTTL > 0 {
		return g.ApprovalTTL
	}
	return 10 * time.Minute
}

// wait 进入审批等待：写待批请求文件（展示面），轮询决定文件，最后
// 消费两个文件并写审计。所有出口（批准/拒绝/超时/取消）都保证
// 清理现场，不留拖尾状态。
func (g *Gate) wait(ctx context.Context, hash string, ex runner.Execution) error {
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return fmt.Errorf("policy: 建审批目录: %w", err)
	}
	now := g.now()
	p := PendingRequest{
		Hash: hash, Script: ex.Script, WorkDir: ex.WorkDir,
		RunID: ex.RunID, TaskID: ex.TaskID, Attempt: ex.Attempt,
		Created: now.UTC(), Expires: now.Add(g.ttl()).UTC(),
		Risks: RiskNotes(ex.Script),
	}
	if err := writeJSONAtomic(g.requestPath(hash), p); err != nil {
		return fmt.Errorf("policy: 写待批请求: %w", err)
	}
	if g.Logger != nil {
		Announce(p, g.Logger)
	}
	if g.WaitHook != nil {
		g.WaitHook(ex, true)
	}
	defer func() {
		// 无论何种结局：请求与决定都被消费（决定晚到也不残留——
		// 陈旧决定绝不能影响下一次等待）。
		_ = os.Remove(g.requestPath(hash))
		_ = os.Remove(g.decisionPath(hash))
		if g.WaitHook != nil {
			g.WaitHook(ex, false)
		}
	}()

	deadline := now.Add(g.ttl())
	tick := time.NewTicker(g.poll())
	defer tick.Stop()
	for {
		if d, ok := readDecision(g.decisionPath(hash)); ok {
			if d.Decision == "approve" {
				g.cache[approvalKey(hash, ex)] = g.now().Add(g.ttl())
				g.audit(p, "approved")
				if g.Logger != nil {
					g.Logger("脚本已批准（hash %s），继续执行", short(p.Hash))
				}
				return nil
			}
			g.audit(p, "denied")
			return fmt.Errorf("%w（用户拒绝）", ErrDenied)
		}
		if !g.now().Before(deadline) {
			g.audit(p, "timeout")
			return ErrTimeout
		}
		select {
		case <-ctx.Done():
			g.audit(p, "canceled")
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (g *Gate) requestPath(hash string) string { return filepath.Join(g.dir, "request-"+hash+".json") }
func (g *Gate) decisionPath(hash string) string {
	return filepath.Join(g.dir, "decision-"+hash+".json")
}

// audit 追加一条授权事实：哈希、归属与决定——绝不含脚本正文或
// 环境凭据。审计写失败不阻塞决策（日志而不是状态）。
func (g *Gate) audit(p PendingRequest, decision string) {
	entry := map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"hash":     p.Hash,
		"work_dir": p.WorkDir,
		"run_id":   p.RunID,
		"task_id":  p.TaskID,
		"attempt":  p.Attempt,
		"decision": decision,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(g.dir, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// short 是哈希的展示前缀（64 位在终端里不可读）。
func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// Announce 把待批请求渲染成等人处理的提示：脚本正文、工作目录、
// 任务/运行归属、风险提示与批准指引。正文完整展示——"执行前展示
// 正文"是 ask 策略的定义。
func Announce(p PendingRequest, logf func(format string, args ...any)) {
	var b strings.Builder
	b.WriteString("━━ 脚本等待执行批准（MINDLOOP_EXEC_POLICY=ask）━━\n")
	b.WriteString("hash: " + short(p.Hash) + "\n")
	b.WriteString("工作目录: " + p.WorkDir + "\n")
	if p.TaskID != "" {
		fmt.Fprintf(&b, "任务: %s（attempt %d）\n", p.TaskID, p.Attempt)
	}
	b.WriteString("运行: " + p.RunID + "\n")
	for _, r := range p.Risks {
		b.WriteString("⚠ " + r + "\n")
	}
	b.WriteString("有效期至 " + p.Expires.Local().Format("15:04:05") +
		"；脚本变化、超时、取消或重启后需重新批准。\n")
	b.WriteString("在另一终端批准: mindloop approve <traj> " + short(p.Hash) + "\n")
	b.WriteString("拒绝:          mindloop approve <traj> " + short(p.Hash) + " --deny\n")
	b.WriteString("── 脚本正文 ──\n")
	b.WriteString(p.Script)
	logf("%s", b.String())
}

// riskPattern 是辅助展示的启发式规则。
type riskPattern struct {
	re   *regexp.Regexp
	note string
}

// riskPatterns 只提示常见的高影响动作。它们不参与放行/拦截决策：
// 关键词黑名单决定"安全"是把风险判断外包给正则——批准与否由人看
// 正文断。提示可能漏报，也可能误报（注释里的词也算命中）。
var riskPatterns = []riskPattern{
	{regexp.MustCompile(`(?i)\brm\s+-[a-z]*[rf]`), "包含删除操作（rm -rf/-f），可能不可逆"},
	{regexp.MustCompile(`(?i)\b(curl|wget)\b`), "访问网络（curl/wget）"},
	{regexp.MustCompile(`(?i)\b(curl|wget)[^|\n]*\|\s*(ba|z|d)?sh\b`), "把网络内容直接管道给 shell 执行"},
	{regexp.MustCompile(`(?i)\bgit\s+(push|reset\s+--hard|clean\s+-)`), "改写远端或丢弃本地变更（git push / reset --hard / clean）"},
	{regexp.MustCompile(`(?i)(\.ssh[/\\]|id_rsa|\.aws[/\\]credentials|\.env\b)`), "可能触及凭据文件（.ssh / .aws / .env）"},
	{regexp.MustCompile(`(?i)\bsudo\b`), "请求提权（sudo）"},
	{regexp.MustCompile(`(?i)\b(npm|pnpm|yarn)\s+publish\b`), "发布到包仓库（publish）"},
}

// RiskNotes 返回脚本正文的辅助风险提示（可能为空）。
func RiskNotes(script string) []string {
	var out []string
	for _, p := range riskPatterns {
		if p.re.MatchString(script) {
			out = append(out, p.note)
		}
	}
	return out
}
