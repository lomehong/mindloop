// 场景 intent 回流端点（scene.md §3.3/§6）：用户在卡片上的有语义
// 交互以 intent 步骤落轨迹（事实源一致），choice 卡关联审批时换回
// 完整 hash 走 policy.Decide——审批桌面化的决策入口。生命周期信号
// （dismiss 等）只落轨迹作上下文，绝不触发执行；本端点永不叫醒心智。
package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/traj"
)

// sceneIntentRequest 是 intent 上行的载荷（scene.md §3.4：surface
// 归属 + kind 定义的意图名 + 载荷）。
type sceneIntentRequest struct {
	Surface string          `json:"surface"`
	Name    string          `json:"name"`
	Data    json.RawMessage `json:"data"`
}

// routeSceneIntent 服务 POST /api/identities/{name}/scene/intent。
func (s *Server) handleSceneIntent(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "intent 只接受 POST")
		return
	}
	var req sceneIntentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "intent 载荷解析失败: "+err.Error())
		return
	}
	if req.Surface == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "surface 与 name 必填")
		return
	}

	// intent 步骤落轨迹：字段扁平（data JSON 字符串化），type 进
	// reservedTypes——结构性步骤，不继承 run id。
	fields := map[string]any{"surface": req.Surface, "name": req.Name}
	if len(req.Data) > 0 {
		fields["data"] = string(req.Data)
	}
	step := traj.NewStep(traj.TypeIntent)
	for k, v := range fields {
		step.Fields[k] = v
	}
	if err := id.Timeline.Append(r.Context(), step); err != nil {
		writeError(w, http.StatusInternalServerError, "intent 落盘失败: "+err.Error())
		return
	}

	// choice→审批闭环：approve-<hash8> 的 select 换回完整 hash 落
	// 决策。ResolveHash 对不存在的待批返回错误（已消费/超时/不存在
	// ——决定晚到不残留的语义）——都表现为 decided=false，卡片由
	// 发卡方（WaitHook 离开回调）撤除，前端不用区分细节。极短窗口
	// 内的重复点击会覆盖决策文件（原子写），消费方只读一次，无碍。
	decided := false
	if req.Name == "select" && strings.HasPrefix(req.Surface, "approve-") {
		value := ""
		var payload struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(req.Data, &payload) == nil {
			value = payload.Value
		}
		if value == "approve" || value == "deny" {
			dir := policy.Dir(id.Timeline.Dir)
			full, err := policy.ResolveHash(dir, strings.TrimPrefix(req.Surface, "approve-"))
			if err == nil && policy.Decide(dir, full, value == "approve") == nil {
				decided = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "decided": decided})
}
