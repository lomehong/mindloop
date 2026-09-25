package mind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// captureThinker 记录收到的系统提示——扩展段接线测试用。
type captureThinker struct {
	mu     sync.Mutex
	system string
}

func (c *captureThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	c.mu.Lock()
	c.system = system
	c.mu.Unlock()
	return fence(`FINAL="IDLE"`), nil
}

func (c *captureThinker) got() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.system
}

// TestMonolithSystemPromptExtensions：技能索引与 MCP 服务器清单
// 接进系统提示（渐进披露的第一层——只进"有什么、怎么探索"）。
func TestMonolithSystemPromptExtensions(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "monolith-ext")
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: demo-skill\ndescription: 演示技能\n---\n正文"), 0o644); err != nil {
		t.Fatal(err)
	}

	th := &captureThinker{}
	m := NewMonolith(MonolithOptions{
		Timeline:   tl,
		Thinker:    th,
		SkillsDirs: []string{filepath.Dir(skillDir)},
		MCPServers: []string{"github", "filesystem"},
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})

	sys := th.got()
	if !strings.Contains(sys, "## Skills") || !strings.Contains(sys, "demo-skill — 演示技能") {
		t.Fatalf("技能索引应进系统提示: %s", sys)
	}
	if !strings.Contains(sys, "## MCP tools") ||
		!strings.Contains(sys, "mcp tools <server>") ||
		!strings.Contains(sys, "github, filesystem") {
		t.Fatalf("MCP 清单应进系统提示: %s", sys)
	}

	// 未配置扩展时提示里不应出现空段。
	th2 := &captureThinker{}
	m2 := NewMonolith(MonolithOptions{Timeline: tl, Thinker: th2})
	m2.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})
	if strings.Contains(th2.got(), "## Skills") || strings.Contains(th2.got(), "## MCP tools") {
		t.Fatalf("无扩展时不应出现空段: %s", th2.got())
	}
}
