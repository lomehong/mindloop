// 场景投影端（scene.md §3.2）：type=scene 步骤的折叠器 + SSE 流。
// 轨迹是唯一事实源——本文件的折叠语义（upsert 覆盖、remove 删除）
// 与 mind.SceneStore.Replay 同一 schema 契约（scene.md §3.1），
// 两端各自实现而不共享代码（跨包共享折叠器会制造 web→mind 依赖边，
// 与"路由即数据"的解耦同构）。
//
// 订阅语义：挂上即发全量快照帧（rev = 折叠后卡片数），此后每条
// scene 步骤转 patch 帧。不走 Last-Event-ID 续接——全量帧开销小
// （≤8 卡），断线重连拿快照是最简单的恢复语义，与 BaiLongma 的
// resync 等价。
package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// sceneSub 是一条场景订阅。帧通道语义与 streamSub 相同：慢订阅者
// 弃用不等待，客户端重连拿全量。
type sceneSub struct {
	ch     chan sseFrame
	lagged bool
}

// sceneFrame 是广播给订阅者的载荷。
type sceneFrame struct {
	event string
	data  string
}

// sceneSurfaces 是折叠状态：id → surface 字段（op 已消解）。无自持
// 锁——并发由 watcher 的 w.mu 统一持有（applySceneLine 在 pollTrail
// 临界区内、snapshot 在订阅临界区内）。
type sceneSurfaces struct {
	surfaces map[string]map[string]any
	rev      int // 折叠过的 scene 步骤计数（快照帧的版本号）
}

// applyScene 把一条 scene 步骤折叠进状态，返回要广播的 patch 帧
// （remove 返回 op=remove，upsert 返回完整 surface）。非 scene 步骤
// 返回 nil。
func (sv *sceneSurfaces) applyScene(line []byte) *sceneFrame {
	var raw struct {
		Type string `json:"type"`
		Op   string `json:"op"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(line, &raw) != nil || raw.Type != traj.TypeScene || raw.ID == "" {
		return nil
	}
	sv.rev++
	if raw.Op == "remove" {
		delete(sv.surfaces, raw.ID)
		payload, _ := json.Marshal(map[string]any{"rev": sv.rev, "ops": []map[string]any{{"op": "remove", "id": raw.ID}}})
		return &sceneFrame{event: "scene.patch", data: string(payload)}
	}
	var fields map[string]any
	if json.Unmarshal(line, &fields) != nil || fields == nil {
		return nil
	}
	// 收窄到协议字段：轨迹行信封（step_id/ts/type）不进投影端。
	surface := map[string]any{}
	for _, k := range []string{"id", "kind", "intent", "summary", "data", "focus"} {
		if v, ok := fields[k]; ok {
			surface[k] = v
		}
	}
	sv.surfaces[raw.ID] = surface
	payload, _ := json.Marshal(map[string]any{"rev": sv.rev, "ops": []map[string]any{{"op": "upsert", "surface": surface}}})
	return &sceneFrame{event: "scene.patch", data: string(payload)}
}

// snapshot 序列化当前折叠态（全量帧）。surfaces 按 id 排序——同一
// 状态永远产生同一字节序，前端 diff 与测试都稳定。
func (sv *sceneSurfaces) snapshot() string {
	ids := make([]string, 0, len(sv.surfaces))
	for id := range sv.surfaces {
		ids = append(ids, id)
	}
	sortStrings(ids)
	list := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, sv.surfaces[id])
	}
	payload, _ := json.Marshal(map[string]any{"rev": sv.rev, "surfaces": list})
	return string(payload)
}

func sortStrings(s []string) { sort.Strings(s) }

// —— watcher 侧挂载 ——

// subscribeScene 是 hub 层的挂载包装：懒创建观察器（与 subscribe
// 同一生命周期管理——最后一个订阅者离开时的释放也走既有路径）。
func (h *streamHub) subscribeScene(id *identity.Identity) (*sceneSub, sseFrame, func()) {
	for {
		h.mu.Lock()
		w := h.byKey[id.Timeline.Dir]
		if w == nil {
			w = newIdentityWatcher(h, id)
			h.byKey[w.key] = w
			go w.run()
		}
		h.mu.Unlock()

		w.mu.Lock()
		if w.stopped {
			// 竞态：观察器恰在释放中——重建后重试。
			w.mu.Unlock()
			continue
		}
		w.initSceneLocked()
		sub := &sceneSub{ch: make(chan sseFrame, w.subCap)}
		w.sceneSubs[sub] = true
		snap := sseFrame{event: "scene", data: w.scene.snapshot()}
		w.mu.Unlock()
		return sub, snap, func() { w.unsubscribeScene(sub) }
	}
}

// initScene 在观察器上初始化场景折叠状态（懒挂载：首次 scene 步骤
// 或首个订阅者时创建）。
func (w *identityWatcher) initSceneLocked() {
	if w.scene == nil {
		w.scene = &sceneSurfaces{surfaces: map[string]map[string]any{}}
	}
	if w.sceneSubs == nil {
		w.sceneSubs = map[*sceneSub]bool{}
	}
}

// applySceneLine 是 pollTrail 的 scene 分支：折叠 + 广播 patch。
// line 已是完整 JSONL 行。
func (w *identityWatcher) applySceneLine(line []byte) {
	w.initSceneLocked()
	if f := w.scene.applyScene(line); f != nil {
		w.emitSceneLocked(*f)
	}
}

// emitSceneLocked 广播一帧给全部场景订阅者（慢订阅者弃用，与
// emitLocked 同语义）。
func (w *identityWatcher) emitSceneLocked(f sceneFrame) {
	w.initSceneLocked()
	for sub := range w.sceneSubs {
		select {
		case sub.ch <- sseFrame{event: f.event, data: f.data}:
		default:
			delete(w.sceneSubs, sub)
			sub.lagged = true
			close(sub.ch)
		}
	}
}

// subscribeScene 挂载一个场景订阅者并返回预热全量帧。同一临界区
// 内完成挂载与快照——订阅之后的新步骤只会以 patch 到达。
func (w *identityWatcher) subscribeScene() (*sceneSub, sseFrame) {
	w.initSceneLocked()
	sub := &sceneSub{ch: make(chan sseFrame, w.subCap)}
	w.mu.Lock()
	w.sceneSubs[sub] = true
	snap := sseFrame{event: "scene", data: w.scene.snapshot()}
	w.mu.Unlock()
	return sub, snap
}

// unsubscribeScene 摘除场景订阅者。场景订阅计入观察器的存活判定：
// replies 与 scene 订阅都为空时观察器释放（与 hub.unsubscribe 的
// 生命周期管理一致——否则纯 scene 订阅会泄漏观察器）。
func (w *identityWatcher) unsubscribeScene(sub *sceneSub) {
	w.mu.Lock()
	if _, ok := w.sceneSubs[sub]; ok {
		delete(w.sceneSubs, sub)
		if !sub.lagged {
			close(sub.ch)
		}
	}
	empty := len(w.subs) == 0 && len(w.sceneSubs) == 0
	if empty && !w.stopped {
		w.stopped = true
		for s := range w.sceneSubs {
			delete(w.sceneSubs, s)
			if !s.lagged {
				close(s.ch)
			}
		}
		close(w.done)
	}
	w.mu.Unlock()
	w.hub.noteDisconnect(w.key)
	if empty {
		w.hub.mu.Lock()
		if w.hub.byKey[w.key] == w {
			delete(w.hub.byKey, w.key)
		}
		w.hub.mu.Unlock()
	}
}

// routeScene 是 /api/identities/{name}/scene 的路由。
func (s *Server) routeScene(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 1 && rest[0] == "stream" {
		s.handleSceneStream(w, r, id)
		return
	}
	if len(rest) == 1 && rest[0] == "intent" {
		s.handleSceneIntent(w, r, id)
		return
	}
	writeError(w, http.StatusNotFound, "未知 scene 端点")
}

// handleSceneStream 服务场景 SSE：订阅 → 全量快照 → patch 按序转发。
// 写期限/ping 语义与 handleRepliesStream 一致。
func (s *Server) handleSceneStream(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	// 预热帧在订阅同一临界区内取得（订阅与全量快照原子）。
	sub, warm, done := s.streams.subscribeScene(id)
	defer done()

	w.WriteHeader(http.StatusOK)
	sw := newSSEWriter(w, sseWriteGrace)
	if err := sw.writeFrame(warm); err != nil {
		return
	}
	ping := s.replyPingEvery
	if ping <= 0 {
		ping = 15 * time.Second
	}
	ticker := time.NewTicker(ping)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f, ok := <-sub.ch:
			if !ok {
				return // 慢订阅者被弃用：重连拿全量
			}
			if err := sw.writeFrame(f); err != nil {
				return
			}
		case <-ticker.C:
			if err := sw.writeComment("ping"); err != nil {
				return
			}
		}
	}
}
