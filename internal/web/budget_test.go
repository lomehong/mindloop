package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
)

func TestBudgetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	ts, _ := newTestServer(t, identity.Home(), "")

	// GET 初始 = 不封顶。
	resp, err := http.Get(ts.URL + "/api/identities/ada/budget")
	if err != nil {
		t.Fatal(err)
	}
	var cfg budgetConfig
	json.NewDecoder(resp.Body).Decode(&cfg)
	resp.Body.Close()
	if cfg.DailyLimit != nil {
		t.Fatalf("初始应为不封顶，得到 %v", *cfg.DailyLimit)
	}

	// PUT 设上限 2000000。
	body := `{"daily_limit": 2000000}`
	resp2, err := http.NewRequest(http.MethodPut, ts.URL+"/api/identities/ada/budget", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(resp2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != 200 {
		t.Fatalf("PUT status = %d", res2.StatusCode)
	}
	res2.Body.Close()

	// GET 确认。
	resp3, err := http.Get(ts.URL + "/api/identities/ada/budget")
	if err != nil {
		t.Fatal(err)
	}
	var cfg2 budgetConfig
	json.NewDecoder(resp3.Body).Decode(&cfg2)
	resp3.Body.Close()
	if cfg2.DailyLimit == nil || *cfg2.DailyLimit != 2000000 {
		t.Fatalf("daily_limit 应为 2000000，得到 %v", cfg2.DailyLimit)
	}

	// PUT null = 不封顶。
	body3 := `{"daily_limit": null}`
	req4, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/identities/ada/budget", strings.NewReader(body3))
	req4.Header.Set("Content-Type", "application/json")
	res4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	res4.Body.Close()

	// GET 确认回到不封顶。
	resp5, err := http.Get(ts.URL + "/api/identities/ada/budget")
	if err != nil {
		t.Fatal(err)
	}
	var cfg5 budgetConfig
	json.NewDecoder(resp5.Body).Decode(&cfg5)
	resp5.Body.Close()
	if cfg5.DailyLimit != nil {
		t.Fatalf("null 后应为不封顶，得到 %v", *cfg5.DailyLimit)
	}
}
