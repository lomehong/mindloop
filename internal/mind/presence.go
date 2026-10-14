// 操作员在场（theater.md §8）：presence 步骤的折叠与唤醒注入段。
// 在场是仪表盘的「在场芯片」落轨迹的产物——轨迹是唯一事实源，
// mind 每次唤醒现场折叠（与 sceneManifest 同款：不维护长驻状态）。
// 折叠结果有两个消费面：monolith 的镜像纪律段（离开时 inform 及
// 以上镜像推企微——由模型按注入的纪律用既有 message→出站泵通道
// 投递，不新增发送边）与 policy/回看的可读状态。镜像默认关：
// MirrorWecomTargets 为空时本文件不注入任何段（设置页开关，
// MINDLOOP_WECOM_MIRROR=1 且 wecom 渠道就绪才有目标）。
package mind

import (
	"fmt"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// PresenceSnapshot 是折叠后的在场状态：away=是否离开，Since=本态
// 起点（离开时刻或最后回到在座的时刻），Zero 值 = 从未声明（视为
// 在座——没有证据就不假装知道操作员不在）。
type PresenceSnapshot struct {
	Away  bool
	Since time.Time
}

// FoldPresence 折叠 presence 步骤流（新→旧取第一条），与场景折叠
// 同一 schema 契约：字段 state=present|away、since=RFC3339。坏行
// 跳过——在场是增强信号，坏数据不能反推出"离开"。
func FoldPresence(steps []traj.Step) PresenceSnapshot {
	snap := PresenceSnapshot{}
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type != traj.TypePresence {
			continue
		}
		state, _ := s.Field("state")
		if state != "away" && state != "present" {
			continue
		}
		snap.Away = state == "away"
		if since, _ := s.Field("since"); since != "" {
			if t, err := time.Parse(time.RFC3339, since); err == nil {
				snap.Since = t
			}
		}
		return snap
	}
	return snap
}

// presenceManifestMaxAge 是注入段的有效期：离开声明超过 24h 后不再
// 注入镜像纪律——隔了天的"离开"多半是忘切回来的陈旧状态，此时静默
// （隔夜账由投影端在操作员回来时合成，不依赖这段）。防陈旧状态
// 长期驱动投递行为。
const presenceManifestMaxAge = 24 * time.Hour

// presenceManifest 渲染镜像纪律段（theater.md §8）：仅当操作员
// 离开、镜像目标非空（默认关）、且离开声明未超龄时非空。目标地址
// 即 monolith 系统提示已披露的 wecom 地址——这里只重复纪律，不新增
// 投递通道；出站边界（未验证观察不得原文外发）由系统提示统一约束。
func presenceManifest(snap PresenceSnapshot, targets []string, now time.Time) string {
	if !snap.Away || len(targets) == 0 {
		return ""
	}
	if snap.Since.IsZero() || now.Sub(snap.Since) > presenceManifestMaxAge {
		return ""
	}
	away := now.Sub(snap.Since)
	awayTxt := formatPresenceDuration(away)
	var b strings.Builder
	fmt.Fprintf(&b, "操作员在场：离开（自 %s 起，已 %s）。离开期间的镜像纪律：",
		snap.Since.Local().Format("15:04"), awayTxt)
	fmt.Fprintf(&b, "你产生的 inform 及以上结论（新上舞台的卡片、收口的任务、需要批准的请求），")
	fmt.Fprintf(&b, "各用一条 message 步骤投递一句话摘要到 %s（既有出站通道）；",
		strings.Join(targets, "、"))
	b.WriteString("ambient 背景卡与例行状态不打扰；摘要给结论与下一步，不倾倒原文。")
	b.WriteString("操作员回来后界面会自动合成隔夜账，无需你补总结。")
	return b.String()
}

// formatPresenceDuration 是在场时长的紧凑读数（47m / 3h 12m）。
func formatPresenceDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
