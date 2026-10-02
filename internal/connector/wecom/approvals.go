package wecom

// 审批卡：把 policy 门（MINDLOOP_EXEC_POLICY=approval）的待批脚本
// 推成 button_interaction 卡片到企微，用户点允许/拒绝，点击回调经
// policy.Decide 落决策（gate 轮询消费后自动继续/拒绝执行），卡片
// 更新为终态。手机上完成全部审批闭环。
//
// 短 token：卡片按钮 key 装不下 64 位脚本哈希，桥铸造 8 位 hex
// 短 token（持久映射），点击回调换回真哈希。点击者必须在白名单
// ——审批权不外借。

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/connector/ws"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/traj"
)

// clickRe 匹配审批按钮 event_key：approve/deny:<短token>。
var clickRe = regexp.MustCompile(`^(approve|deny):([0-9a-f]{8})$`)

// runApprovals 轮询待批目录直到 ctx 取消：新请求发审批卡（已发过
// 的 hash 不重发；桥重启会重发一次——同一审批两张卡，先点的生效，
// 后一张点击落决策会被 gate 的"决定晚到不残留"语义安全忽略）。
func (b *Bridge) runApprovals(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	sent := map[string]bool{} // hash → 已发卡
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			dir := policy.Dir(b.opts.Timeline.Dir)
			pending, err := policy.ListPending(dir)
			if err != nil {
				continue
			}
			for _, p := range pending {
				if sent[p.Hash] {
					continue
				}
				sent[p.Hash] = true
				b.sendApprovalCard(ctx, dir, p.Hash, p)
			}
		}
	}
}

// sendApprovalCard 发送一张审批卡到审批目标地址（缺省白名单第一人）。
func (b *Bridge) sendApprovalCard(ctx context.Context, dir, hash string, p policy.PendingRequest) {
	c := b.currentConn()
	if c == nil {
		b.logf("审批卡未发送（连接未就绪）: %s", shortHash(hash))
		return
	}
	token := b.tokens.mint(hash)
	to := b.approveTo()
	card := approvalCard{}
	card.TaskID = "mindloop-appr-" + token
	card.Source = "mindloop"
	card.Title = "🔐 工具执行审批"
	card.Sub = firstLine(p.Script) + scriptMeta(p)
	card.Allow.Text, card.Allow.Key = "允许", keyApprove+token
	card.Deny.Text, card.Deny.Key = "拒绝", keyDeny+token
	reqID := newReqID(cmdSendMsg)
	if err := c.WriteMessage(ws.OpText, cardSendFrame(strings.TrimPrefix(to, "wecom:"), card, reqID)); err != nil {
		b.logf("审批卡发送失败: %v", err)
		return
	}
	resp, err := b.awaitResponse(ctx, reqID)
	if err != nil {
		b.logf("审批卡回执缺失: %v", err)
		return
	}
	if resp.Errcode != 0 {
		b.logf("审批卡被拒（errcode=%d）: %s", resp.Errcode, resp.Errmsg)
		return
	}
	b.logf("审批卡已发送（%s → %s）", shortHash(hash), to)
}

// handleCardClick 处理审批卡点击：白名单校验 → 换回哈希 → 待批仍
// 存在才落决策 → 卡片更新为终态（透传点击回调的 req_id）。
func (b *Bridge) handleCardClick(ctx context.Context, f parsedFrame, ev eventBody) {
	m := clickRe.FindStringSubmatch(ev.cardEventKey())
	if m == nil {
		return
	}
	decision, token := m[1], m[2]
	if !b.allow[ev.Event.From.Userid] {
		b.logf("白名单外用户 %q 的审批点击已忽略", ev.Event.From.Userid)
		return
	}
	hash := b.tokens.resolve(token)
	if hash == "" {
		return // 未知 token：陈旧卡片的残留点击
	}
	dir := policy.Dir(b.opts.Timeline.Dir)
	var card approvalCard
	card.TaskID = "mindloop-appr-" + token
	card.Source = "mindloop"
	// 待批还在：落决策；已消失（超时/已处理）：只把卡片定稿。
	still, _ := policy.ListPending(dir)
	pending := false
	var req policy.PendingRequest
	for _, p := range still {
		if p.Hash == hash {
			pending = true
			req = p
		}
	}
	if pending {
		if err := policy.Decide(dir, hash, decision == "approve"); err != nil {
			b.logf("审批决策落盘失败: %v", err)
			return
		}
		// 味觉落轨迹（持久证据；失败只记日志——决定已生效）。
		signal := "approve"
		if decision != "approve" {
			signal = "deny"
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := traj.AppendTasteStep(ctx, b.opts.Timeline, signal, "", hash); err != nil {
			b.logf("味觉归因落盘失败（决定已生效）: %v", err)
		}
	}
	outcome := "已拒绝"
	if decision == "approve" {
		outcome = "已允许"
	}
	card.Allow.Text = outcome + "（点击无副作用）"
	card.Allow.Key = "done:" + token
	card.Title = "🔐 工具执行审批 · " + outcome
	card.Sub = "本审批已结束"
	if pending {
		card.Sub = firstLine(req.Script)
	}
	if err := b.respondCardUpdate(ctx, f.ReqID, card); err != nil {
		b.logf("卡片定稿失败（决策本身已生效）: %v", err)
	}
	b.logf("审批 %s： %s", shortHash(hash), outcome)
}

// respondCardUpdate 推送卡片定稿帧并等回执。
func (b *Bridge) respondCardUpdate(ctx context.Context, reqID string, card approvalCard) error {
	c := b.currentConn()
	if c == nil {
		return errors.New("wecom: 连接未就绪")
	}
	if err := c.WriteMessage(ws.OpText, cardUpdateFrame(reqID, card)); err != nil {
		return err
	}
	resp, err := b.awaitResponse(ctx, reqID)
	if err != nil {
		return err
	}
	if resp.Errcode != 0 {
		return errors.New("update 失败（errcode=" + strconv.FormatInt(resp.Errcode, 10) + "）: " + resp.Errmsg)
	}
	return nil
}

// approveTo 是审批卡的投递目标：显式配置优先（WECOM_APPROVE_TO），
// 缺省白名单第一人（个人场景即本人）。
func (b *Bridge) approveTo() string {
	if v := strings.TrimSpace(b.opts.ApproveTo); v != "" {
		return v
	}
	return "wecom:" + b.firstAllow()
}

func (b *Bridge) firstAllow() string {
	for uid := range b.allow {
		return uid
	}
	return ""
}

// firstLine 取脚本首行（审批卡副标题；空脚本给占位）。
func firstLine(script string) string {
	script = strings.TrimSpace(script)
	if script == "" {
		return "(空脚本)"
	}
	if i := strings.IndexByte(script, '\n'); i >= 0 {
		return script[:i]
	}
	return script
}

// scriptMeta 拼接风险注记与工作目录（副标题第二行起）。
func scriptMeta(p policy.PendingRequest) string {
	parts := make([]string, 0, 2)
	if len(p.Risks) > 0 {
		parts = append(parts, "风险: "+strings.Join(p.Risks, "、"))
	}
	if p.WorkDir != "" {
		parts = append(parts, "目录: "+p.WorkDir)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n" + strings.Join(parts, " · ")
}

// shortHash 是日志用的哈希前缀。
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
