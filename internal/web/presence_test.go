package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestPresenceRoundTrip：POST away → GET 折叠回 away（presence 步骤
// 落轨迹 = 跨设备真相）；POST present 翻回；GET 未声明身份返回
// unstated（前端此时用本机缓存，不冒充服务端已知）。
func TestPresenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	ts, srv := newTestServer(t, identity.Home(), "")

	post := func(body string) map[string]any {
		req := httptest.NewRequest(http.MethodPost,
			ts.URL+"/api/identities/ada/presence", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.routePresence(rec, req, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("presence POST status = %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	get := func() presenceView {
		req := httptest.NewRequest(http.MethodGet,
			ts.URL+"/api/identities/ada/presence", nil)
		rec := httptest.NewRecorder()
		srv.routePresence(rec, req, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("presence GET status = %d", rec.Code)
		}
		var out presenceView
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// 未声明：unstated
	if v := get(); !v.Unstated || v.State != "present" {
		t.Fatalf("未声明应 unstated+present，got %+v", v)
	}

	// 切离开：POST 响应与 GET 折叠一致，since 取本态起点
	out := post(`{"away":true}`)
	if out["state"] != "away" {
		t.Fatalf("POST 响应 state = %v", out["state"])
	}
	v := get()
	if v.State != "away" || v.Unstated {
		t.Fatalf("GET 应 away 且已声明，got %+v", v)
	}
	if since, err := time.Parse(time.RFC3339, v.SinceTS); err != nil || time.Since(since) > time.Minute {
		t.Fatalf("since_ts 应为最近时刻，got %q", v.SinceTS)
	}

	// 翻回在座
	post(`{"away":false}`)
	if v := get(); v.State != "present" {
		t.Fatalf("翻回后应 present，got %+v", v)
	}

	// 轨迹里确实是 presence 结构性步骤
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range steps {
		if s.Type == traj.TypePresence {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("presence 步骤数 = %d, want 2", n)
	}

	// 非法方法
	req := httptest.NewRequest(http.MethodDelete, ts.URL+"/api/identities/ada/presence", nil)
	rec := httptest.NewRecorder()
	srv.routePresence(rec, req, id)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE 应 405，got %d", rec.Code)
	}
}
