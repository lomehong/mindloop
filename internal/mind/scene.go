// SceneStore 是界面场景的进程内缓存（scene.md §2/§5）：轨迹是唯一
// 事实源（type=scene 步骤追加流），Store 只承担三件轨迹做不了的事——
// set 前幂等判重（无变化不落步骤）、密度上限硬约束（8 张）、manifest
// 回注的派生。重启从轨迹 replay 重建，状态漂移不可能。
//
// 写侧（ui set → Store.Upsert/Remove → Timeline.Append）与读侧
// （wakeTask manifest、web 投影端各自的轨迹折叠）互不知晓——web 不
// 依赖本包，它自己折叠轨迹；两端的折叠语义必须一致，由 schema 约定
// （scene.md §3.1）而不是共享代码约束（跨包共享折叠器会制造 mind→web
// 依赖边）。
package mind

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/lomehong/mindloop/internal/traj"
)

// sceneMaxSurfaces 是场景密度上限：卡片面的价值在克制，堆到十几张
// 既吃投影端注意力也吃 manifest 预算（BaiLongma 同款数字）。
const sceneMaxSurfaces = 8

// maxSceneDataBytes / maxSceneSummaryBytes 是 set 的载荷上限（写盘
// 与回注双保护）。
const (
	maxSceneDataBytes    = 16 * 1024
	maxSceneSummaryBytes = 80
)

// Surface 是场景单元的内存形态（轨迹 fields 的结构化视图）。
type Surface struct {
	ID      string
	Kind    string
	Intent  string // ambient | inform | confront；空按 inform
	Focus   bool
	Summary string
	Data    json.RawMessage
}

// fingerprint 是幂等判重的键：kind+intent+focus+summary+data 的
// canonical 摘要。同 id 同指纹 = 无变化。
func (s *Surface) fingerprint() string {
	h := sha256.Sum256([]byte(s.Kind + "\x00" + s.Intent + "\x00" +
		fmt.Sprint(s.Focus) + "\x00" + s.Summary + "\x00" + string(s.Data)))
	return hex.EncodeToString(h[:])
}

// SceneStore 是一个身份的场景缓存。并发安全（ui set 经 CLI 子命令
// 进程调用会并发到达？——CLI 是独立进程，Append 走目录锁；Store 挂
// 在 monolith，同进程内只有 wakeTask 读。仍上锁：写侧判重与落轨迹
// 之间不能有竞态窗口）。
type SceneStore struct {
	mu       sync.Mutex
	surfaces map[string]*Surface
}

// NewSceneStore 建空场景；调用方应先 Replay 轨迹历史。
func NewSceneStore() *SceneStore {
	return &SceneStore{surfaces: map[string]*Surface{}}
}

// sceneFromStep 把 scene 步骤解析为 Surface。isRemove=true 时
// surface 为 nil（调用方按 id 删除）。
func sceneFromStep(s traj.Step) (surface *Surface, isRemove bool) {
	op, _ := s.Field("op")
	if op == "remove" {
		return nil, true
	}
	id, _ := s.Field("id")
	if id == "" {
		return nil, false
	}
	surface = &Surface{ID: id}
	surface.Kind, _ = s.Field("kind")
	surface.Intent, _ = s.Field("intent")
	surface.Summary, _ = s.Field("summary")
	if f, ok := s.Field("focus"); ok {
		surface.Focus = f == "true" // Field 把 bool 折叠成 "true"/"false"
	}
	if raw, ok := s.Field("data"); ok {
		surface.Data = json.RawMessage(raw)
	}
	return surface, false
}

// Replay 从轨迹步骤流重建场景（upsert 覆盖、remove 删除——折叠
// 语义与投影端一致，schema 是唯一契约）。
func (st *SceneStore) Replay(steps []traj.Step) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range steps {
		if s.Type != traj.TypeScene {
			continue
		}
		surface, isRemove := sceneFromStep(s)
		if isRemove {
			id, _ := s.Field("id")
			delete(st.surfaces, id)
			continue
		}
		if surface == nil {
			continue
		}
		st.surfaces[surface.ID] = surface
	}
}

// Upsert 幂等声明一个 surface：无变化返回 changed=false（调用方不落
// 轨迹）；超密度上限返回错误。data 必须已是合法 JSON。
func (st *SceneStore) Upsert(s Surface) (changed bool, err error) {
	if s.ID == "" || s.Kind == "" {
		return false, fmt.Errorf("scene: id 与 kind 必填")
	}
	if len(s.Data) > maxSceneDataBytes {
		return false, fmt.Errorf("scene: data 超限（%d > %d 字节）", len(s.Data), maxSceneDataBytes)
	}
	if len(s.Summary) > maxSceneSummaryBytes {
		s.Summary = s.Summary[:maxSceneSummaryBytes]
	}
	if s.Intent != "" && s.Intent != "ambient" && s.Intent != "inform" && s.Intent != "confront" {
		return false, fmt.Errorf("scene: intent 只允许 ambient/inform/confront")
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if cur, ok := st.surfaces[s.ID]; ok && cur.fingerprint() == s.fingerprint() {
		return false, nil
	}
	if _, exists := st.surfaces[s.ID]; !exists && len(st.surfaces) >= sceneMaxSurfaces {
		return false, fmt.Errorf("scene: 场景已满（%d 张）——先移除不再需要的卡片（ui list 查看）", sceneMaxSurfaces)
	}
	st.surfaces[s.ID] = &s
	return true, nil
}

// Remove 移除一个 surface；不存在返回 changed=false。
func (st *SceneStore) Remove(id string) (changed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.surfaces[id]; !ok {
		return false
	}
	delete(st.surfaces, id)
	return true
}

// Manifest 把当前场景渲染成回注段（scene.md §5.2）：id + kind +
// 一行摘要 + intent——只注入"舞台上有什么"，绝不注入 data 全文。
// 空场景返回空串。 confront/focus 卡带 ←焦点 标记。
func (st *SceneStore) Manifest() string {
	st.mu.Lock()
	list := make([]*Surface, 0, len(st.surfaces))
	for _, s := range st.surfaces {
		list = append(list, s)
	}
	st.mu.Unlock()
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	var b strings.Builder
	b.WriteString("当前界面卡片（id 供更新/撤卡引用；用户看得到这些，说话要一致）：")
	for _, s := range list {
		intent := s.Intent
		if intent == "" {
			intent = "inform"
		}
		marker := ""
		if s.Focus {
			marker = " ←焦点"
		}
		fmt.Fprintf(&b, "\n- %s (%s) 「%s」 %s%s", s.ID, s.Kind, s.Summary, intent, marker)
	}
	return b.String()
}
