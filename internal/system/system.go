// Package system 实现系统宿主：按 <home>/system.json 的声明监督
// 全部能力子进程（web / 每身份心智 / 每身份渠道桥），配置即期望
// 状态，宿主负责兑现与维持（docs/designs/system.md）。
//
// 职责边界：宿主只监督进程，不内嵌任何能力逻辑——桥与心智的进程
// 隔离不变式保留；配置细节（sensors/schedule/.env）仍是各能力的
// 事实源，本包只管"什么在跑"。
package system

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lomehong/mindloop/internal/identity"
)

// Config 是 system.json 的形态：能力总开关。
type Config struct {
	Version    int              `json:"version"`
	Web        WebConfig        `json:"web"`
	Identities map[string]Ident `json:"identities"`
}

// WebConfig 是仪表盘能力开关。
type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host,omitempty"` // 缺省 127.0.0.1
	Port    int    `json:"port,omitempty"` // 缺省 8080
}

// Ident 是一个身份的能力开关。
type Ident struct {
	Enabled bool `json:"enabled"` // 总开关：false 即该身份全部能力停
	Mind    bool `json:"mind"`    // 心智（调度器）
	Wecom   bool `json:"wecom"`   // 企微渠道桥（凭据就绪才会派生为期望）
}

// ChildName 是宿主监督的子进程身份名（也是状态投影与日志的键）。
type ChildName string

const (
	WebChild ChildName = "web"
)

// MindChild / WecomChild 构造每身份子进程键。
func MindChild(identity string) ChildName  { return ChildName("mind:" + identity) }
func WecomChild(identity string) ChildName { return ChildName("wecom:" + identity) }

// IdentityOf 从子进程键还原身份名；非每身份子进程返回 ""。
func IdentityOf(name ChildName) string {
	if i := strings.IndexByte(string(name), ':'); i >= 0 {
		return string(name)[i+1:]
	}
	return ""
}

// Path 是 system.json 的位置。
func Path(home string) string { return filepath.Join(home, "system.json") }

// StatusPath 是状态投影的位置。
func StatusPath(home string) string { return filepath.Join(home, "system-status.json") }

// Load 读 system.json；文件缺失返回零值 config（调用方走 Defaults
// 推导——零配置即用）；坏配置返回错误（fail-closed：宿主拒绝按半份
// 配置跑）。
func Load(home string) (*Config, error) {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("system: 读配置失败: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("system: 配置不是合法 JSON: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save 原子写回（校验前置）。
func Save(home string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Version == 0 {
		c.Version = 1
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := Path(home)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate 校验：端口范围、身份名非空。
func (c *Config) Validate() error {
	if c.Web.Port != 0 && (c.Web.Port < 1 || c.Web.Port > 65535) {
		return fmt.Errorf("system: web.port 非法（1-65535）")
	}
	for name := range c.Identities {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("system: 身份名不能为空")
		}
	}
	return nil
}

// IsEmpty 报告配置是否为零值（文件缺失时 Load 的结果）——调用方
// 应转 Defaults() 推导（零配置即用）。
func (c *Config) IsEmpty() bool {
	return c.Version == 0 && len(c.Identities) == 0 && !c.Web.Enabled
}

// Defaults 按现状推导零配置缺省：web 开在本机回环；全部身份开
// mind；有企微凭据的身份开 wecom（凭据 = 显式环境 > 身份 .env，
// 与 connector 装配同源）。零配置即用——装好 init 的人 system run
// 一步到位。
func Defaults() *Config {
	c := &Config{
		Version:    1,
		Web:        WebConfig{Enabled: true, Host: "127.0.0.1", Port: 8080},
		Identities: map[string]Ident{},
	}
	list, err := identity.List()
	if err != nil {
		return c
	}
	for _, name := range list {
		id, err := identity.Load(name)
		if err != nil {
			continue
		}
		c.Identities[name] = Ident{
			Enabled: true,
			Mind:    true,
			Wecom:   wecomReady(id),
		}
	}
	return c
}

func wecomReady(id *identity.Identity) bool {
	botID := firstNonEmpty(os.Getenv("WECOM_BOT_ID"), envValue(id, "WECOM_BOT_ID"))
	secret := firstNonEmpty(os.Getenv("WECOM_BOT_SECRET"), envValue(id, "WECOM_BOT_SECRET"))
	allow := firstNonEmpty(os.Getenv("WECOM_ALLOW"), envValue(id, "WECOM_ALLOW"))
	return botID != "" && secret != "" && allow != ""
}

// envValue 读身份 .env 的键（不落进程环境——只探测）。
func envValue(id *identity.Identity, key string) string {
	path := filepath.Join(id.Dir, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(strings.Trim(v, `"'`))
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Plan 是一次期望状态 diff：要启动的与要停止的子进程键。
type Plan struct {
	Start map[ChildName]bool
	Stop  map[ChildName]bool
}

// Diff 对比配置与当前在跑的子进程集，产出增停计划（热加载的
// 决策纯函数；监督循环执行它）。
func (c *Config) Diff(running map[ChildName]bool) Plan {
	desired := c.Desired()
	p := Plan{Start: map[ChildName]bool{}, Stop: map[ChildName]bool{}}
	for name := range desired {
		if !running[name] {
			p.Start[name] = true
		}
	}
	for name := range running {
		if !desired[name] {
			p.Stop[name] = true
		}
	}
	return p
}

// Desired 从配置推导期望子进程集。
func (c *Config) Desired() map[ChildName]bool {
	out := map[ChildName]bool{}
	if c.Web.Enabled {
		out[WebChild] = true
	}
	names := make([]string, 0, len(c.Identities))
	for name := range c.Identities {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		id := c.Identities[name]
		if !id.Enabled {
			continue
		}
		if id.Mind {
			out[MindChild(name)] = true
		}
		if id.Wecom {
			out[WecomChild(name)] = true
		}
	}
	return out
}

// Args 是子进程的命令行（self-exec 既有命令——宿主不内嵌能力逻辑）。
func Args(name ChildName, c *Config) []string {
	switch {
	case name == WebChild:
		host := c.Web.Host
		if host == "" {
			host = "127.0.0.1"
		}
		port := c.Web.Port
		if port == 0 {
			port = 8080
		}
		return []string{"web", "--host", host, "--port", fmt.Sprintf("%d", port)}
	case strings.HasPrefix(string(name), "mind:"):
		return []string{"mind", "run", IdentityOf(name)}
	case strings.HasPrefix(string(name), "wecom:"):
		return []string{"connector", "run", "--identity", IdentityOf(name)}
	}
	return nil
}
