// Package update 实现「检测升级」：消费 GitHub Releases（goreleaser
// 产物），对当前版本做真实比对，按部署形态走各自的升级路径。
//
// 三条铁律（avatar.md 纪律在系统层的延伸）：
//  1. 检测可以自动（后台定时），安装必须人点头——POST install 只在
//     操作员显式点击后由端点触发，本包绝不自主发起安装；
//  2. 升级前必过 checksums.txt 的 sha256 校验，校验失败宁可不升级；
//  3. 读数诚实：检测失败如实携带 error，绝不谎报"已是最新"。
//
// 部署形态与升级路径：
//   - 桌面壳内（MINDLOOP_SHELL=1，sidecar 由 Tauri 壳拉起）：下载
//     NSIS 安装包并启动——安装器接管退出/升级/重启；
//   - 二进制/服务化（exe 路径被计划任务固化，改名前缀 .old 不破坏
//     绑定）：下载 zip → 校验 → 就地替换（改名舞步），重启生效由
//     操作员执行（mindloop service restart 或手动重启）；
//   - robotd 随 zip 分发（goreleaser 双构建）：仅当它与 mindloop
//     同目录时一并替换（运行中的 robotd 保留旧版至下次拉起）。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultRepo 是发布仓库；MINDLOOP_RELEASE_REPO 可覆盖（自建镜像）。
const DefaultRepo = "lomehong/mindloop"

// Asset 是 release 里的单个下载产物。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release 是 releases/latest 的消费子集。
type Release struct {
	Tag       string    `json:"tag_name"`
	URL       string    `json:"html_url"`
	Published time.Time `json:"published_at"`
	Notes     string    `json:"body"`
	Assets    []Asset   `json:"assets"`
}

// Info 是对外（web 端点/前端）的检测读数契约。
type Info struct {
	CheckedAt   time.Time `json:"checked_at"`
	Current     string    `json:"current"`
	LatestTag   string    `json:"latest_tag"`
	HasUpdate   bool      `json:"has_update"`
	URL         string    `json:"url"`
	PublishedAt string    `json:"published_at,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	// Error 非空 = 这次检测失败（网络/限流）——读数诚实，绝不把
	// 失败渲染成"已是最新"。
	Error string `json:"error,omitempty"`
}

// ---- 版本比对 ---------------------------------------------------------------
// 零第三方依赖：tag 形如 v0.15.0 / 0.15.0，按数字段逐段比较。非数字
// 段按 0 处理（候选 tag 带后缀时不误报升级）。

func normalizeTag(tag string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "v")
	tag = strings.TrimPrefix(tag, "V")
	return tag
}

func segments(v string) []int {
	parts := strings.Split(normalizeTag(v), ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// HasNewer 报告 latest 是否严格新于 current（只认数字段；相等不升级）。
func HasNewer(current, latest string) bool {
	c, l := segments(current), segments(latest)
	n := len(c)
	if len(l) > n {
		n = len(l)
	}
	for i := 0; i < n; i++ {
		var a, b int
		if i < len(c) {
			a = c[i]
		}
		if i < len(l) {
			b = l[i]
		}
		if b != a {
			return b > a
		}
	}
	return false
}

// ---- 检测 -------------------------------------------------------------------

// Checker 缓存最近一次检测结果并按周期后台刷新。零值不可用，走 New。
type Checker struct {
	mu       sync.Mutex
	current  string
	repo     string
	interval time.Duration
	client   *http.Client
	fetch    func(ctx context.Context, repo string) (Release, error)

	last    Info
	lastRel Release
}

// NewChecker 装配检测器；interval <= 0 表示不跑后台（仅手动触发）。
// 周期驱动在 Run(ctx)——生命周期归调用方。
func NewChecker(current, repo string, interval time.Duration) *Checker {
	c := &Checker{
		current:  current,
		repo:     repo,
		interval: interval,
		client:   &http.Client{Timeout: 20 * time.Second},
	}
	c.fetch = c.fetchGitHub
	return c
}

// Run 后台周期检测：立即检测一次，此后每 interval 一次；ctx 取消即停。
// interval <= 0 时本方法不做任何事（手动 CheckNow 仍可用）。
func (c *Checker) Run(ctx context.Context) {
	if c.interval <= 0 {
		return
	}
	c.CheckNow(ctx)
	tick := time.NewTicker(c.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c.CheckNow(ctx)
		}
	}
}

// Snapshot 返回最近一次检测读数（从未检测过时 HasUpdate=false 且
// CheckedAt 零值——前端按"尚未检测"渲染）。
func (c *Checker) Snapshot() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Version 返回装配时给定的当前版本（web 端点响应用，避免 web→cli
// 反向依赖）。
func (c *Checker) Version() string { return c.current }

// LatestRelease 返回最近一次检测到的完整 Release（含资产清单，供
// 安装编排取下载地址）；缓存缺失或与读数不一致时现场重取。
func (c *Checker) LatestRelease(ctx context.Context) (Release, error) {
	c.mu.Lock()
	rel := c.lastRel
	tag := c.last.LatestTag
	c.mu.Unlock()
	if rel.Tag != "" && rel.Tag == tag {
		return rel, nil
	}
	rel, err := c.fetch(ctx, c.repo)
	if err != nil {
		return Release{}, err
	}
	c.mu.Lock()
	c.lastRel = rel
	c.mu.Unlock()
	return rel, nil
}

// CheckNow 立即检测并缓存结果。
func (c *Checker) CheckNow(ctx context.Context) Info {
	rel, err := c.fetch(ctx, c.repo)
	info := Info{Current: c.current, CheckedAt: time.Now()}
	if err != nil {
		info.Error = err.Error()
	} else {
		info.LatestTag = rel.Tag
		info.HasUpdate = HasNewer(c.current, rel.Tag)
		info.URL = rel.URL
		if !rel.Published.IsZero() {
			info.PublishedAt = rel.Published.Format(time.RFC3339)
		}
		info.Notes = excerpt(rel.Notes, 280)
	}
	c.mu.Lock()
	c.last = info
	if err == nil {
		c.lastRel = rel
	}
	c.mu.Unlock()
	return info
}

func (c *Checker) fetchGitHub(ctx context.Context, repo string) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	// GitHub API 要求 User-Agent；Accept 钉住稳定版 JSON。
	req.Header.Set("User-Agent", "mindloop-update")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("仓库还没有任何 release（%s）", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub API %d（限流或网络问题时稍后重试）", resp.StatusCode)
	}
	var raw struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Body        string    `json:"body"`
		Assets      []Asset   `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return Release{}, err
	}
	if raw.TagName == "" {
		return Release{}, errors.New("release 缺 tag_name")
	}
	return Release{Tag: raw.TagName, URL: raw.HTMLURL, Published: raw.PublishedAt, Notes: raw.Body, Assets: raw.Assets}, nil
}

func excerpt(s string, max int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
