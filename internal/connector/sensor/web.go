// web.go — 网页感官（眼）：URL/RSS 的条件请求轮询。SSRF 防线是
// 本设计的自建规格（perception.md §5 Phase 1，评审核实全仓无既有
// 出站白名单可复用）：
//   - 拨号时复核 IP（net.Dialer.Control 在连接建立前拿到解析后的
//     地址）——私网/回环/链路本地/未指定一律拒连；复核发生在
//     connect 时刻而非 resolve 时刻，DNS rebinding 的 TOCTOU 窗口
//     不存在；30x 重定向每一跳都重新走拨号 → 逐跳覆盖；
//   - 响应体截断（512KB），digest 只取正文前段——观察数据不是
//     下载通道。
package sensor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// webBodyLimit 是单次响应体的读取上限——感官是观察者不是爬虫。
const webBodyLimit = 512 << 10

// webIntervalMin 是轮询间隔下限：对外部站点的礼貌，也是成本闸。
const webIntervalMin = 5 * time.Minute

// WebSensor 轮询一个 http(s) URL 的内容变化。
type WebSensor struct {
	cfg       SensorConfig
	client    *http.Client
	seen      map[string]bool // feed 条目指纹（RSS/Atom 语义模式的已见集）
	baselined bool            // 首扫已建基线（基线不发历史积压）
}

// NewWebSensor 构造网页感官（含 SSRF 防线的拨号层）。
func NewWebSensor(cfg SensorConfig) (Sensor, error) {
	if cfg.Type != "web" {
		return nil, fmt.Errorf("sensor: %s 不是 web 类型", cfg.ID)
	}
	u := strings.TrimSpace(cfg.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return nil, fmt.Errorf("sensor: %s 的 url 必须 http(s):// 开头", cfg.ID)
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("web 感官: 无法解析地址 %q", address)
			}
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("web 感官: 拒连私网/回环地址 %s（SSRF 防线）", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{DialContext: dialer.DialContext}
	return &WebSensor{cfg: cfg, client: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, seen: map[string]bool{}}, nil
}

func (s *WebSensor) ID() string { return s.cfg.ID }

func (s *WebSensor) Watch(ctx context.Context, onEvent func(PEvent)) error {
	interval := s.cfg.IntervalOf(webIntervalMin)
	if interval < webIntervalMin {
		interval = webIntervalMin
	}
	var lastFP, lastETag, lastMod string
	var lastFail string
	first := true
	tick := time.NewTicker(interval)
	defer tick.Stop()

	fetch := func() bool { // 返回 false = ctx 取消
		select {
		case <-ctx.Done():
			return false
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.URL, nil)
		if err != nil {
			return true
		}
		req.Header.Set("User-Agent", "mindloop-sensor/0.8 (+local observer)")
		if lastETag != "" {
			req.Header.Set("If-None-Match", lastETag)
		}
		if lastMod != "" {
			req.Header.Set("If-Modified-Since", lastMod)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			s.reportFailure(ctx, onEvent, &lastFail, err.Error())
			return true
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotModified {
			return true // 条件请求命中：内容未变
		}
		if resp.StatusCode >= 400 {
			s.reportFailure(ctx, onEvent, &lastFail, fmt.Sprintf("HTTP %d", resp.StatusCode))
			return true
		}
		if s.reportFailure(ctx, onEvent, &lastFail, "") {
			// 故障恢复：报一条恢复事件，dedup 绑定恢复时刻。
			onEvent(PEvent{Kind: KindChanged, Subject: s.cfg.URL,
				Dedup:  "recovered-" + time.Now().Format("2006-01-02-1504"),
				Digest: "观察目标恢复正常（此前 " + lastFail + "）"})
		}
		lastETag = resp.Header.Get("ETag")
		lastMod = resp.Header.Get("Last-Modified")
		body, err := io.ReadAll(io.LimitReader(resp.Body, webBodyLimit+1))
		if err != nil {
			return true
		}
		sum := sha256.Sum256(body)
		fp := hex.EncodeToString(sum[:16])
		if first {
			lastFP = fp
			first = false
			return true
		}
		if fp == lastFP {
			return true
		}
		lastFP = fp
		// feed 语义（RSS/Atom）：逐条目事件（新条目 = appeared，
		// dedup = 条目 GUID）——比整页指纹精确一级，旧条目不重报。
		if items, ok := parseFeed(body); ok && len(items) > 0 {
			s.feedDiff(items, onEvent)
			return true
		}
		onEvent(PEvent{
			Kind: KindChanged, Subject: s.cfg.URL, Dedup: fp,
			Digest: "内容更新: " + strings.TrimSpace(extractText(body)),
		})
		return true
	}
	fetch()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !fetch() {
				return ctx.Err()
			}
		}
	}
}

// feedItem 是 RSS/Atom 条目的归一形态。
type feedItem struct {
	ID    string // guid/id/link——条目身份
	Title string
}

// parseFeed 识别并解析 RSS 2.0 / Atom。不是 feed 返回 ok=false
// （调用方回退整页指纹路径）。两套 envelope 字段名不同，分开定义
// （同结构体挂同名 XML 元素会冲突）。
func parseFeed(body []byte) ([]feedItem, bool) {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	probe := strings.ToLower(string(head))
	if !strings.Contains(probe, "<rss") && !strings.Contains(probe, "<feed") {
		return nil, false
	}
	var items []feedItem
	var rss struct {
		Channel struct {
			Items []struct {
				GUID  string `xml:"guid"`
				Title string `xml:"title"`
				Link  string `xml:"link"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		for _, e := range rss.Channel.Items {
			id := e.GUID
			if id == "" {
				id = e.Link
			}
			if id == "" {
				id = e.Title
			}
			if id == "" {
				continue
			}
			items = append(items, feedItem{ID: id, Title: e.Title})
		}
		return items, true
	}
	var atom struct {
		Entries []struct {
			ID    string `xml:"id"`
			Title string `xml:"title"`
			Links []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		for _, e := range atom.Entries {
			id := e.ID
			if id == "" && len(e.Links) > 0 {
				id = e.Links[0].Href
			}
			if id == "" {
				id = e.Title
			}
			if id == "" {
				continue
			}
			items = append(items, feedItem{ID: id, Title: e.Title})
		}
		return items, true
	}
	return nil, false
}

// feedDiff 把 feed 条目与已见集 diff：新条目逐条 appeared（摘要=
// 标题），消失不报（feed 常截断历史）。基线期只建集不发声。
func (s *WebSensor) feedDiff(items []feedItem, onEvent func(PEvent)) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	var fresh []feedItem
	for _, it := range items {
		if !s.seen[it.ID] {
			s.seen[it.ID] = true
			fresh = append(fresh, it)
		}
	}
	if !s.baselined {
		s.baselined = true
		return // 基线：首扫只登记，不发历史积压
	}
	for _, it := range fresh {
		title := it.Title
		if runes := []rune(title); len(runes) > 120 {
			title = string(runes[:120])
		}
		onEvent(PEvent{
			Kind: KindAppeared, Subject: s.cfg.URL, Dedup: it.ID,
			Digest: "新条目: " + title,
		})
	}
}

// reportFailure 是故障态迁移检测：只在 ok→fail / fail→fail(新原因)
// / fail→ok 边沿发事件，不逐轮刷屏。返回值 = 本次是否从故障恢复。
func (s *WebSensor) reportFailure(ctx context.Context, onEvent func(PEvent), lastFail *string, cause string) bool {
	if cause == "" {
		if *lastFail != "" {
			*lastFail = ""
			return true
		}
		return false
	}
	if *lastFail == cause {
		return false // 同一故障不重复报（状态迁移检测）
	}
	*lastFail = cause
	onEvent(PEvent{
		Kind: KindAnomaly, Subject: s.cfg.URL,
		Dedup:  "fail-" + time.Now().Format("2006-01-02-15"), // 同小时合并且判定层还有去重窗
		Digest: "观察目标不可达: " + cause,
	})
	return false
}

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe  = regexp.MustCompile(`\s+`)
)

// extractText 从 HTML/文本响应提取正文前段（digest 的原料；
// 信任分界由写位 wrapDigest 统一加）。
func extractText(body []byte) string {
	t := scriptRe.ReplaceAllString(string(body), " ")
	t = tagRe.ReplaceAllString(t, " ")
	t = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(t)
	t = spaceRe.ReplaceAllString(t, " ")
	runes := []rune(strings.TrimSpace(t))
	if len(runes) > 160 {
		runes = runes[:160]
	}
	return string(runes)
}
