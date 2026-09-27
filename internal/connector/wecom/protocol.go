package wecom

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// 协议字段的唯一出处，按官方 @wecom/aibot-node-sdk v1.0.7 的线上
// 实现校准（参考项目 dsh-im-bot/im-channel/lib/channels/wecom）：
// 帧形态、命令名、心跳与回执语义与 SDK 逐项对齐——对接真实网关时
// 若有出入，改动面收敛于此，桥本体不动。
//
// 帧形态（SDK 线上事实）：
//   - 开发者→企微：{ cmd, headers: { req_id }, body }——每帧必须
//     带 req_id（SDK 前缀_毫秒_随机串）；
//   - 企微推送：{ cmd: aibot_msg_callback / aibot_event_callback,
//     headers: { req_id }, body: {...} }；
//   - 回执（订阅/心跳/发送的响应）：无 cmd——
//     { headers: { req_id }, errcode, errmsg }，errcode=0 为成功。

const (
	cmdSubscribe     = "aibot_subscribe"          // 认证订阅
	cmdPing          = "ping"                     // 应用层心跳（不是 WS 层 ping）
	cmdSendMsg       = "aibot_send_msg"           // 主动推送
	cmdRespondMsg    = "aibot_respond_msg"        // 被动回复（流式，透传回调 req_id）
	cmdRespondUpdate = "aibot_respond_update_msg" // 卡片更新（透传点击回调 req_id）
	cmdMsgCallback   = "aibot_msg_callback"       // 消息推送
	cmdEventCallback = "aibot_event_callback"     // 事件推送
)

// eventTypeDisconnected 是单连接互踢：aibot_event_callback 的
// body.event.eventtype——嵌套在事件载荷里，不是独立 cmd。
const eventTypeDisconnected = "disconnected_event"

// eventTypeCard 是审批卡按钮点击事件；event_key 前缀见 keys。
const eventTypeCard = "template_card_event"

const (
	keyApprove = "approve:"
	keyDeny    = "deny:"
)

// envelope 是一帧业务形态（开发者发出与企微推送共用）。
type envelope struct {
	Cmd     string `json:"cmd"`
	Headers struct {
		ReqID string `json:"req_id"`
	} `json:"headers"`
	Body json.RawMessage `json:"body"`
}

// callbackBody 是 aibot_msg_callback 的载荷子集——本桥消费的字段
// （msgid、发送者 userid、文本内容）；图片/语音等不解析不透传。
type callbackBody struct {
	Msgid    string `json:"msgid"`
	Chattype string `json:"chattype"`
	From     struct {
		Userid string `json:"userid"`
	} `json:"from"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

// eventBody 是 aibot_event_callback 的载荷子集：互踢检测与审批卡
// 点击（template_card_event 的 event_key——线上为嵌套结构
// event.template_card_event.event_key，平铺路径做兼容回退；点击者
// userid 供白名单校验）。
type eventBody struct {
	Event struct {
		EventType         string `json:"eventtype"`
		TemplateCardEvent struct {
			EventKey string `json:"event_key"`
		} `json:"template_card_event"`
		EventKey string `json:"event_key"`
		From     struct {
			Userid string `json:"userid"`
		} `json:"from"`
	} `json:"event"`
}

// cardEventKey 提取卡片点击的 event_key（嵌套优先，平铺回退）。
func (e eventBody) cardEventKey() string {
	if v := e.Event.TemplateCardEvent.EventKey; v != "" {
		return v
	}
	return e.Event.EventKey
}

// parsedFrame 是解帧结果：业务帧（有 cmd）与回执帧（无 cmd，按
// req_id 对账）二类。
type parsedFrame struct {
	IsResponse bool
	Cmd        string
	ReqID      string
	Errcode    int64
	Errmsg     string
	Body       json.RawMessage
}

// parseFrame 解一帧。有 cmd 即业务帧（body 原样保留给分发）；
// 无 cmd 即回执帧，解析 errcode/errmsg。
func parseFrame(data []byte) (parsedFrame, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return parsedFrame{}, fmt.Errorf("wecom: 帧不是合法 JSON: %w", err)
	}
	out := parsedFrame{Cmd: env.Cmd, ReqID: env.Headers.ReqID, Body: env.Body}
	if env.Cmd != "" {
		return out, nil
	}
	var resp struct {
		Errcode int64  `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(data, &resp) // 回执键缺失时零值即"成功"语义
	out.IsResponse = true
	out.Errcode = resp.Errcode
	out.Errmsg = resp.Errmsg
	return out, nil
}

// newReqID 生成请求 id（对齐 SDK：前缀_毫秒时间戳_随机串）。
func newReqID(prefix string) string {
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	return prefix + "_" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + hex.EncodeToString(rnd[:])
}

// marshalFrame 序列化一帧；载荷全是 string/map[string]string，
// 序列化错误实际不可达。
func marshalFrame(env envelope) []byte {
	out, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return out
}

// subscribeFrame 构造认证订阅帧：bot_id + 长连接专用 secret（非
// 回调 Token/EncodingAESKey——那两个属于公网回调形态，本桥不用）。
func subscribeFrame(botID, secret, reqID string) []byte {
	body, _ := json.Marshal(map[string]string{"bot_id": botID, "secret": secret})
	env := envelope{Cmd: cmdSubscribe, Body: body}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}

// pingFrame 构造应用层心跳帧。SDK 语义：连续 2 次无 pong 回执判
// 连接死亡（maxMissedPong=2）——不是 WS 层的 ping/pong。
func pingFrame(reqID string) []byte {
	env := envelope{Cmd: cmdPing}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}

// sendMsgFrame 构造主动推送帧：chatid=userid 单聊，msgtype=markdown
// （主动推送原生支持；用户须已解锁会话）。
func sendMsgFrame(chatid, content, reqID string) []byte {
	body, _ := json.Marshal(map[string]any{
		"chatid":   chatid,
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": content},
	})
	env := envelope{Cmd: cmdSendMsg, Body: body}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}

// streamRespondFrame 构造被动流式回复帧：透传**原回调**的 req_id，
// stream.content 为全量累积文本（客户端整段替换），finish=true 终稿。
// streamId 由桥生成并在同一回合保持稳定。
func streamRespondFrame(reqID, streamID, content string, finish bool) []byte {
	body, _ := json.Marshal(map[string]any{
		"msgtype": "stream",
		"stream": map[string]any{
			"id":      streamID,
			"finish":  finish,
			"content": content,
		},
	})
	env := envelope{Cmd: cmdRespondMsg, Body: body}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}

// approvalCard 是审批按钮卡（button_interaction）的渲染形态：定稿
// 前后复用同一结构——定稿帧把按钮收敛为单个结果按钮（企微要求
// button_list 非空），Allow.Text 承载终态文案。
type approvalCard struct {
	TaskID string
	Source string
	Title  string
	Sub    string
	Allow  struct {
		Text, Key string
	}
	Deny struct {
		Text, Key string
	}
}

// cardSendFrame 构造模板卡片主动推送帧（button_interaction 审批卡）。
// task_id 全局唯一（点击回调会携带它）。
func cardSendFrame(chatid string, card approvalCard, reqID string) []byte {
	body, _ := json.Marshal(map[string]any{
		"chatid":  chatid,
		"msgtype": "template_card",
		"template_card": map[string]any{
			"card_type":      "button_interaction",
			"task_id":        card.TaskID,
			"source":         map[string]string{"desc": card.Source},
			"main_title":     map[string]string{"title": card.Title},
			"sub_title_text": card.Sub,
			"button_list": []map[string]any{
				{"text": card.Allow.Text, "key": card.Allow.Key, "style": 1},
				{"text": card.Deny.Text, "key": card.Deny.Key, "style": 2},
			},
		},
	})
	env := envelope{Cmd: cmdSendMsg, Body: body}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}

// cardUpdateFrame 构造卡片定稿帧（aibot_respond_update_msg）：透传
// **点击回调**的 req_id，response_type=update_template_card。
func cardUpdateFrame(reqID string, card approvalCard) []byte {
	body, _ := json.Marshal(map[string]any{
		"response_type": "update_template_card",
		"template_card": map[string]any{
			"card_type":      "button_interaction",
			"task_id":        card.TaskID,
			"source":         map[string]string{"desc": card.Source},
			"main_title":     map[string]string{"title": card.Title},
			"sub_title_text": card.Sub,
			"button_list":    []map[string]any{{"text": card.Allow.Text, "key": card.Allow.Key, "style": 1}},
		},
	})
	env := envelope{Cmd: cmdRespondUpdate, Body: body}
	env.Headers.ReqID = reqID
	return marshalFrame(env)
}
