package mind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// 流式回复的瞬态产物：旁路全文文件与 replying 状态文件。两者都是
// "重启即无主"的崩溃垃圾——dispatcher 启动时的 gcTransients 统一
// 清场（调用方持运行锁，此刻无调度器在跑，与 ClearStopFlag 同一
// 模式）。轨迹是唯一事实源：最终 message 步骤照旧一次性落盘，旁
// 路文件只是实时投影视图，读方以文件全量为准、消失即为 done。

// streamDir 流式旁路目录：<Timeline.Dir>/stream。
func streamDir(tlDir string) string { return filepath.Join(tlDir, "stream") }

// replyingPath 回复状态文件：<RunLockDir>/replying（运行锁目录内，
// 调度器存活期即其生命周期，崩溃后由 GC 兜底）。
func replyingPath(tlDir string) string { return filepath.Join(RunLockDir(tlDir), "replying") }

// replyingState 是 replying 状态文件的 JSON 形态——web SSE 的
// status 事件数据源（契约由 Lead 固定，web 层照此解析）。
type replyingState struct {
	ReplyTo string `json:"reply_to"`
	Thinker string `json:"thinker"`
	Since   string `json:"since"` // RFC3339
}

// workingPath 工作状态文件：<RunLockDir>/working（与 replying 同层）。
// 文件存在 ⇔ 忙集非空 ⇔ 有思考者正在工作——对话页据此给出确定性
// 的"工作中"信号，而不是靠猜。
func workingPath(tlDir string) string { return filepath.Join(RunLockDir(tlDir), "working") }

// workingEntry 是忙集里的一条在途唤醒记录。
type workingEntry struct {
	Thinker string `json:"thinker"`
	Wake    string `json:"wake"`  // step | watchdog | scheduled
	Since   string `json:"since"` // RFC3339
}

// workingState 是 working 状态文件的 JSON 形态（契约由 Lead 固定）。
type workingState struct {
	Working bool           `json:"working"`
	Busy    []workingEntry `json:"busy"`
}

// writeWorkingFile 全量重写工作状态文件。投影写失败容忍：控制面
// 信号不值得打断调度——读方最多少看一轮状态。
func writeWorkingFile(tlDir string, busy []workingEntry) {
	payload, err := json.Marshal(workingState{Working: true, Busy: busy})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(workingPath(tlDir)), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(workingPath(tlDir), payload, 0o644)
}

// defaultReplyDwell 是旁路文件的最小可观测驻留默认值。快生成器
// （echo、缓存命中、短回复）的整个回复生命周期可能不足几十毫秒——
// 短于读方（web SSE 200ms 轮询）的采样周期，旁路成了"瞬态不可见"，
// 流式协议静默失效（lead 端到端实测：echo 回复 ~80ms，25s 窗口内
// 零 delta/零 done）。驻留足额后清理，读方至少能采到一轮。
const defaultReplyDwell = time.Second

// replyStream 管理一次回复的旁路文件与 replying 状态的生命周期：
//
//	begin → appendDelta… → seal（全文定稿）→ [轨迹 append] → finish
//	任何失败走 discard（两文件一起消失，web 侧表现为"没有在回复"，
//	与未流式时代的失败语义一致）。
//
// 方法全部容忍 nil 接收者——调用方无需重复判空。
type replyStream struct {
	tlDir   string
	replyTo string
	f       *os.File // seal 之后为 nil
	begin   time.Time
	dwell   time.Duration // 最小可观测驻留；≤0 = 删即走
}

// beginReplyStream 建立旁路：截断式创建 <stream>/<reply_to>.txt 并
// 写入 replying 状态。任何一步失败返回 nil——流式是体验增强，绝不
// 因旁路建立失败挡住回复本身（调用方退化为一次性补全）。
func beginReplyStream(tlDir, replyTo string, dwell time.Duration) *replyStream {
	if !safeStepID(replyTo) {
		return nil // reply_to 进文件名：不合规的 id 宁可不流式
	}
	if err := os.MkdirAll(streamDir(tlDir), 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(streamDir(tlDir), replyTo+".txt"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil
	}
	s := &replyStream{tlDir: tlDir, replyTo: replyTo, f: f, begin: time.Now(), dwell: dwell}
	// replying 住在运行锁目录内：生产中持锁时目录必然存在，但这里
	// 自建父目录让旁路在任何调用形态下都自洽（测试、嵌入方）。
	if err := os.MkdirAll(filepath.Dir(replyingPath(tlDir)), 0o755); err != nil {
		f.Close()
		_ = os.Remove(s.sidecarPath())
		return nil
	}
	if err := s.markReplying(); err != nil {
		f.Close()
		_ = os.Remove(s.sidecarPath())
		return nil
	}
	return s
}

// appendDelta 把增量文本追加进旁路文件。读方以文件全量为准，所以
// 写失败只意味着读方最多少看几段——不打断回复，也不惊扰思考者。
func (s *replyStream) appendDelta(delta string) {
	if s == nil || s.f == nil {
		return
	}
	_, _ = s.f.WriteString(delta)
}

// seal 定稿：全文不再变化，关闭句柄把数据交还 OS。调用方在轨迹
// append 成功后调 finish，失败走 discard。
func (s *replyStream) seal() {
	if s == nil || s.f == nil {
		return
	}
	s.f.Close()
	s.f = nil
}

// finish 常规收尾：删旁路 + 删 replying。调用约定：正式消息已落
// 轨迹之后才调用——读方看到旁路消失即 done，invalidate 查询必能
// 拿到正式气泡。
//
// 最小可观测驻留：存活不足 dwell 时先补足再删（阻塞发生在 thinker
// 自己的 goroutine，至多一个 dwell，不占调度心跳）。dwell 只延迟
// 清理、不延迟回复落盘——顺序锚不变，前端拿到正式消息至多晚一个
// dwell（默认 1s），换取流式协议对快生成器可见。
func (s *replyStream) finish() {
	if s == nil {
		return
	}
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
	if remaining := s.dwell - time.Since(s.begin); remaining > 0 {
		time.Sleep(remaining)
	}
	_ = os.Remove(s.sidecarPath())
	_ = os.Remove(replyingPath(s.tlDir))
}

// discard 失败收尾：与 finish 同形——两文件一起消失。
func (s *replyStream) discard() { s.finish() }

func (s *replyStream) sidecarPath() string {
	return filepath.Join(streamDir(s.tlDir), s.replyTo+".txt")
}

// markReplying 写入 replying 状态（开始回复前调用）。
func (s *replyStream) markReplying() error {
	payload, err := json.Marshal(replyingState{
		ReplyTo: s.replyTo,
		Thinker: responderName,
		Since:   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(replyingPath(s.tlDir), payload, 0o644)
}

// safeStepID 防路径注入：reply_to 直接拼进文件名，只接受 UUID
// 字符集（小写十六进制与连字符）——手改日志里塞进来的
// "..\\..\\x" 在这里被拒绝，宁可不流式。
func safeStepID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && r != '-' {
			return false
		}
	}
	return true
}

// gcTransients 清空崩溃残留的流式旁路、replying 与 working 状态。
// 调用方必须已持运行锁——此刻无调度器在跑，残留必为垃圾，直接整
// 目录删除。dispatcher.Run 启动时自调用（与 ClearStopFlag 的启动
// 清理同模式）。
func gcTransients(tlDir string) {
	_ = os.RemoveAll(streamDir(tlDir))
	_ = os.Remove(replyingPath(tlDir))
	_ = os.Remove(workingPath(tlDir))
}
