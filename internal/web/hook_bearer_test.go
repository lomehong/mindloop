package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
)

// P1-11 端到端：配置 bearer 的感官——HMAC 正确但缺/错 Bearer 拒绝，
// 双凭据齐对才放行（D7a：非回环部署的纵深防御）。
func TestHookBearerEnforced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const secret = "hook-secret-fixture"
	const bearer = "bearer-secret-fixture"
	cfg := fmt.Sprintf(`{"version":1,"sensors":[{"id":"hookb","type":"webhook","secret":%q,"bearer":%q,"learning_days":-1}]}`, secret, bearer)
	if err := os.WriteFile(filepath.Join(id.Dir, "sensors.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("写 sensors.json: %v", err)
	}
	ts, _ := newTestServer(t, home, "")

	post := func(authHeader string, body string) int {
		now := nowUnix()
		sig := hookSignature(secret, now, []byte(body))
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/hook/ada/hookb", strings.NewReader(body))
		req.Header.Set("X-Mindloop-Timestamp", now)
		req.Header.Set("X-Mindloop-Signature", "sha256="+sig)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	body := fmt.Sprintf(`{"text":"bearer 纵深","run":%d}`, time.Now().UnixNano())

	if code := post("", body); code != http.StatusUnauthorized {
		t.Fatalf("缺 Authorization 应 401，得 %d", code)
	}
	if code := post("Bearer wrong", body); code != http.StatusUnauthorized {
		t.Fatalf("错 Bearer 应 401，得 %d", code)
	}
	if code := post("Bearer "+bearer, body); code != http.StatusNoContent {
		t.Fatalf("双凭据齐对应 204，得 %d", code)
	}
}
