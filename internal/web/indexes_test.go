package web

// 共享侧索引（web 进程内的 indexStore）测试：同一身份的多个高流量
// 请求共享一个 traj.Index；外部写入（追加/替换）对下一次请求立即
// 可见——共享不牺牲新鲜度。

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// chatMessages 解析 ChatLog 契约的 messages 子集。
type chatMessages struct {
	Messages []struct {
		StepID  string `json:"step_id"`
		Content string `json:"content"`
	} `json:"messages"`
}

func getChat(t *testing.T, url string) chatMessages {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat = %d", resp.StatusCode)
	}
	var out chatMessages
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// getJSON GET 并解码；非 200 直接失败——坏输入下聚合端点必须
// 以 200 与诚实数据回应，不得 500。
func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d（坏输入不得打挂聚合）", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

// appendRawLine 直接向轨迹 journal 追加原始行——模拟磁盘上的
// 坏行等真实输入（traj.Append 只写合法步骤）。
func appendRawLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// TestSideIndexSharedAcrossRequests：白盒——status/chat/activity/
// thinkers/tree/列表各打一轮后，同一轨迹路径在进程内恰有一个索引
// 实例（"同一身份多个标签页只共享一套观察"的进程内根基）。
func TestSideIndexSharedAcrossRequests(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	addMessage(t, id, "operator", "ada", "hi")
	ts, s := newTestServer(t, identity.Home(), "")

	for _, path := range []string{
		"/api/identities/ada/status",
		"/api/identities/ada/activity",
		"/api/identities/ada/chat",
		"/api/identities/ada/thinkers",
		"/api/identities/ada/tree?depth=1",
		"/api/identities",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
	}

	s.indexes.mu.Lock()
	n := len(s.indexes.byPath)
	_, ok := s.indexes.byPath[id.Timeline.Path]
	s.indexes.mu.Unlock()
	if n != 1 || !ok {
		t.Fatalf("共享索引应有恰 1 个实例（主路径命中 %v），得到 %d 个", ok, n)
	}
}

// TestSharedIndexSeesExternalWrites：黑盒——共享索引不牺牲新鲜度：
// 外部追加立即可见；rename 替换后请求看到重建事实（而非缓存旧消息）。
func TestSharedIndexSeesExternalWrites(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	addMessage(t, id, "operator", "ada", "m1")
	ts, _ := newTestServer(t, identity.Home(), "")
	url := ts.URL + "/api/identities/ada/chat"

	if got := getChat(t, url); len(got.Messages) != 1 || got.Messages[0].Content != "m1" {
		t.Fatalf("首轮 = %+v", got.Messages)
	}

	// 外部追加：下一次请求立即可见（读请求先追平）。
	addMessage(t, id, "operator", "ada", "m2")
	if got := getChat(t, url); len(got.Messages) != 2 || got.Messages[1].Content != "m2" {
		t.Fatalf("追加后 = %+v", got.Messages)
	}

	// rename 替换整份 journal（保留头行）：共享实例检测到身份变化，
	// 整体重建后看到替换后的事实。
	orig, err := os.ReadFile(id.Timeline.Path)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(string(orig), "\n", 2)[0]
	replaced := `{"type":"message","step_id":"msg-replaced","ts":"2026-01-01T00:00:00.000Z","from":"operator","to":"ada","content":"replaced"}`
	swap := id.Timeline.Path + ".swap"
	if err := os.WriteFile(swap, []byte(head+"\n"+replaced+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swap, id.Timeline.Path); err != nil {
		t.Fatal(err)
	}

	if got := getChat(t, url); len(got.Messages) != 1 || got.Messages[0].Content != "replaced" {
		t.Fatalf("替换后 = %+v（应看到重建事实）", got.Messages)
	}
}

// TestActivityBusyThinkerLastWins：busy_thinkers 取最后一条非空
// launched_by——接线到侧索引后语义不变（重构安全网）。
func TestActivityBusyThinkerLastWins(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, by := range []string{"monolith", "responder"} {
		s := traj.NewStep("action")
		s.Fields["launched_by"] = by
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/activity")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		BusyThinkers []string `json:"busy_thinkers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.BusyThinkers) != 1 || got.BusyThinkers[0] != "responder" {
		t.Fatalf("busy_thinkers = %v，应为 [responder]（最后一条 launched_by 胜出）", got.BusyThinkers)
	}
}

// TestBadInputsToleratedByAggregates：坏行与坏时间戳不得打挂被
// d2 接线到共享索引的聚合路径——坏行按 Steps 语义跳过（计数一致），
// 坏 TS 计入步数但不得被当作活跃事实（thinkers 状态保持 idle）。
func TestBadInputsToleratedByAggregates(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	addMessage(t, id, "operator", "ada", "m1")
	appendRawLine(t, id.Timeline.Path,
		"{broken json\n"+
			// 坏行：缺 ts 键——ParseStep 拒绝（与 Steps 一致的跳过）。
			`{"type":"message","step_id":"msg00000099"}`+"\n"+
			// 坏 TS：ts 键存在（ParseStep 通过）但不可解析——计入步数，
			// 但 thinker 聚合不得把它当作活跃时间。
			`{"type":"action","step_id":"act00000099","ts":"not-a-time","launched_by":"responder"}`+"\n")
	addMessage(t, id, "operator", "ada", "m2")

	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	wantSteps := len(steps)

	ts, _ := newTestServer(t, identity.Home(), "")

	// status：step_count 与 Steps 语义一致（坏行跳过、坏 TS 计入）。
	var status struct {
		StepCount int `json:"step_count"`
	}
	getJSON(t, ts.URL+"/api/identities/ada/status", &status)
	if status.StepCount != wantSteps {
		t.Fatalf("status.step_count = %d，Steps 计数 = %d（坏行跳过、坏 TS 计入）", status.StepCount, wantSteps)
	}

	// chat：坏行不进消息流；坏 TS 是 action，也不进。
	if got := getChat(t, ts.URL+"/api/identities/ada/chat"); len(got.Messages) != 2 ||
		got.Messages[0].Content != "m1" || got.Messages[1].Content != "m2" {
		t.Fatalf("chat.messages = %+v，应为 [m1 m2]", got.Messages)
	}

	// thinkers：坏 TS 不作活跃事实——live 心跳窗内仍判 idle。
	var think struct {
		ThinkersTotal int `json:"thinkers_total"`
		Thinkers      []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"thinkers"`
	}
	getJSON(t, ts.URL+"/api/identities/ada/thinkers", &think)
	if think.ThinkersTotal != 1 || len(think.Thinkers) != 1 {
		t.Fatalf("thinkers_total = %d，应为 1（responder）", think.ThinkersTotal)
	}
	if think.Thinkers[0].State != "idle" {
		t.Fatalf("坏 TS 不得判活跃：state = %q", think.Thinkers[0].State)
	}

	// activity：busy_thinkers 来自最后一条 launched_by（不解析 TS）。
	var act struct {
		BusyThinkers []string `json:"busy_thinkers"`
	}
	getJSON(t, ts.URL+"/api/identities/ada/activity", &act)
	if len(act.BusyThinkers) != 1 || act.BusyThinkers[0] != "responder" {
		t.Fatalf("busy_thinkers = %v，应为 [responder]", act.BusyThinkers)
	}

	// tree：根节点计数与 status 一致。
	var tree struct {
		StepCount int `json:"step_count"`
	}
	getJSON(t, ts.URL+"/api/identities/ada/tree?depth=1", &tree)
	if tree.StepCount != wantSteps {
		t.Fatalf("tree.step_count = %d，应为 %d", tree.StepCount, wantSteps)
	}
}

// TestIdentitiesTolerateCorruptTrajectory：轨迹头行之后含坏行的
// 身份不得打挂列表——它仍出现（头行可读），坏行按 Steps 语义跳过，
// 健康身份不受影响。（头行本身损坏时 LoadAt 丢弃该目录——既有
// 身份加载语义，不在聚合层容忍范围。）
func TestIdentitiesTolerateCorruptTrajectory(t *testing.T) {
	newIdentityHome(t)
	ada, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := identity.Create(context.Background(), "bob"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// ada 的轨迹保留头行，尾部追加坏行（非法 JSON + 缺 ts 键）。
	orig, err := os.ReadFile(ada.Timeline.Path)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(string(orig), "\n", 2)[0]
	damaged := head + "\n" + "{broken json\n" + `{"type":"message"}` + "\n"
	if err := os.WriteFile(ada.Timeline.Path, []byte(damaged), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	var infos []struct {
		ID        string `json:"id"`
		StepCount int    `json:"step_count"`
	}
	getJSON(t, ts.URL+"/api/identities", &infos)
	byID := map[string]int{}
	for _, in := range infos {
		byID[in.ID] = in.StepCount
	}
	count, ok := byID["ada"]
	if !ok {
		t.Fatalf("含坏行的身份仍应出现在列表中: %v", infos)
	}
	if count != 1 {
		t.Fatalf("坏行应被跳过，step_count 应为 1（仅头步），得到 %d", count)
	}
	if _, ok := byID["bob"]; !ok {
		t.Fatalf("健康身份应不受影响: %v", infos)
	}
}
