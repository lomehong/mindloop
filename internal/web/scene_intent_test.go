package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestSceneIntentApprovalLoop：审批桌面化的决策闭环（scene.md §6）——
// gate 写待批请求 → intent select(approve-<hash8>) 换回完整 hash 落
// 决策（decided=true）→ 决策文件可读；决定晚到（待批已消费）语义
// decided=false；intent 步骤落轨迹。
func TestSceneIntentApprovalLoop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	ts, srv := newTestServer(t, identity.Home(), "")

	script := "rm -rf /tmp/some-target"
	hash := policy.ScriptHash(script)
	pdir := policy.Dir(id.Timeline.Dir)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pending, _ := json.Marshal(policy.PendingRequest{
		Hash: hash, Script: script, WorkDir: "C:/tmp",
		Created: now, Expires: now.Add(5 * time.Minute),
	})
	if err := os.WriteFile(filepath.Join(pdir, "request-"+hash+".json"), pending, 0o644); err != nil {
		t.Fatal(err)
	}

	post := func(payload string) map[string]any {
		req := httptest.NewRequest(http.MethodPost,
			ts.URL+"/api/identities/ada/scene/intent", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		srv.handleSceneIntent(rec, req, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("intent POST status = %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// 决策回流：approve-<hash8> → ResolveHash → Decide。
	out := post(`{"surface":"approve-` + hash[:8] + `","name":"select","data":{"value":"approve"}}`)
	if out["decided"] != true {
		t.Fatalf("审批 intent 应落决策: %v", out)
	}
	raw, err := os.ReadFile(filepath.Join(pdir, "decision-"+hash+".json"))
	if err != nil {
		t.Fatalf("决策文件未落盘: %v", err)
	}
	if !strings.Contains(string(raw), "approve") {
		t.Fatalf("决策应为 approve: %s", raw)
	}

	// intent 步骤已落轨迹。
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if s.Type == traj.TypeIntent {
			found = true
			if surface, _ := s.Field("surface"); surface != "approve-"+hash[:8] {
				t.Fatalf("intent surface = %q", surface)
			}
		}
	}
	if !found {
		t.Fatal("intent 步骤未落轨迹")
	}

	// 决定晚到（待批已消费）：gate 的 defer 会删 request 文件——
	// 模拟消费后 ResolveHash 找不到待批，decided=false 而非报错。
	if err := os.Remove(filepath.Join(pdir, "request-"+hash+".json")); err != nil {
		t.Fatal(err)
	}
	out = post(`{"surface":"approve-` + hash[:8] + `","name":"select","data":{"value":"deny"}}`)
	if out["decided"] != false {
		t.Fatalf("决定晚到应 decided=false: %v", out)
	}

	// 非审批 surface：只落轨迹，decided=false。
	out = post(`{"surface":"weather-bj","name":"dismiss","data":{}}`)
	if out["decided"] != false {
		t.Fatalf("非审批 intent 不应 decided: %v", out)
	}

	// 缺 surface：400。
	req := httptest.NewRequest(http.MethodPost, ts.URL+"/api/identities/ada/scene/intent",
		strings.NewReader(`{"name":"select"}`))
	rec := httptest.NewRecorder()
	srv.handleSceneIntent(rec, req, id)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 surface 应 400，得 %d", rec.Code)
	}
}
