package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"mindloop/internal/prompt"
)

func TestCollectFieldFlags(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		known  map[string]bool
		rest   []string
		fields []string
	}{
		{
			name:   "key-value 形态收编",
			args:   []string{"id", "--from", "operator", "--content", "hi"},
			known:  map[string]bool{"content": true},
			rest:   []string{"id", "--content", "hi"},
			fields: []string{"from=operator"},
		},
		{
			// 约定：值以横线开头时必须用 --to=-x 等号形态；空格形态
			// 会被视为裸布尔旗标，后面的 -ada 留给 pflag 去报错。
			name:   "裸旗标吞不下横线开头的值",
			args:   []string{"--json={\"a\":1}", "--flagged", "--to", "-ada"},
			known:  map[string]bool{},
			rest:   []string{"-ada"},
			fields: []string{`json={"a":1}`, "flagged=true", "to=true"},
		},
		{
			name:   "等号形态的值可以以横线开头",
			args:   []string{"--from=-x"},
			known:  map[string]bool{},
			rest:   nil,
			fields: []string{"from=-x"},
		},
		{
			name:   "已知旗标原样放行",
			args:   []string{"--content", "keep", "x"},
			known:  map[string]bool{"content": true},
			rest:   []string{"--content", "keep", "x"},
			fields: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, fields := collectFieldFlags(tc.args, tc.known)
			if !reflect.DeepEqual(rest, tc.rest) {
				t.Fatalf("rest = %v，应为 %v", rest, tc.rest)
			}
			if !reflect.DeepEqual(fields, tc.fields) {
				t.Fatalf("fields = %v，应为 %v", fields, tc.fields)
			}
		})
	}
}

func TestParseValue(t *testing.T) {
	if got := parseValue("42"); got != "42" {
		t.Fatalf("纯文本被误解析: %#v", got)
	}
	var obj any = parseValue(`{"a":1}`)
	m, ok := obj.(map[string]any)
	if !ok || m["a"] != float64(1) {
		t.Fatalf("JSON 对象未解析: %#v", obj)
	}
	var arr any = parseValue("[1,2]")
	if _, ok := arr.([]any); !ok {
		t.Fatalf("JSON 数组未解析: %#v", arr)
	}
	if got := parseValue("[bad json"); got != "[bad json" {
		t.Fatalf("非法 JSON 应原样保留: %#v", got)
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV("a, b,,c"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("splitCSV = %v", got)
	}
	if got := splitCSV(""); got != nil {
		t.Fatalf("空串应返回 nil，得到 %v", got)
	}
}

func TestMessageJSONShape(t *testing.T) {
	// Message 的 JSON 标签是未来 llm 工具的输入契约，钉死形态。
	b, err := json.Marshal([]prompt.Message{{Role: prompt.RoleUser, Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `[{"role":"user","content":"x"}]` {
		t.Fatalf("消息 JSON 形态错误: %s", b)
	}
}

// newTestHome 建立一个测试专用的状态根目录。同一条测试里的多次
// runCLI 必须共享它——轨迹在第一次调用时创建，后续调用要能找到。
func newTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	return home
}

// runCLI 是测试内执行整条 CLI 的辅助（HOME 由 newTestHome 预设）。
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Execute(context.Background(), args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// TestBareInvocationShowsQuickStart：裸调用 = 想知道怎么用，
// 退出码必须是 0，且必须给出可照抄的快速开始。
func TestBareInvocationShowsQuickStart(t *testing.T) {
	newTestHome(t)
	code, out, _ := runCLI(t)
	if code != 0 {
		t.Fatalf("裸调用 exit = %d（应为 0）", code)
	}
	if !strings.Contains(out, "快速开始") || !strings.Contains(out, "chat ada") {
		t.Fatalf("帮助缺少快速开始: %q", out[:min(len(out), 200)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestInterspersedFlags 钉死 pflag 的混排语义：位置参数在前、
// 旗标在后（以及反过来）都必须解析成功——这是迁移到 cobra 的
// 收益之一，曾是手写解析器最大的维护负担。
func TestInterspersedFlags(t *testing.T) {
	newTestHome(t)
	code, out, errOut := runCLI(t, "traj", "new", "--slug", "flags")
	if code != 0 {
		t.Fatalf("new: %d %s", code, errOut)
	}
	id := strings.TrimSpace(out)

	code, out, errOut = runCLI(t, "traj", "append", id, "message", "--from", "operator", "--content", "你好")
	if code != 0 {
		t.Fatalf("append: %d %s", code, errOut)
	}

	// 位置参数在前、旗标在后
	code, out, _ = runCLI(t, "traj", "tail", id, "-n", "1", "--pretty")
	if code != 0 {
		t.Fatalf("tail(旗标在后): %d", code)
	}
	if !strings.Contains(out, "operator") {
		t.Fatalf("tail 输出缺载荷: %q", out)
	}
	// 旗标在前、位置参数在后
	code, _, _ = runCLI(t, "traj", "tail", "-n", "1", "--type", "message", id)
	if code != 0 {
		t.Fatal("tail(旗标在前) 失败")
	}
}

// TestUnknownCommandSuggests：cobra 的 did-you-mean 建议。
func TestUnknownCommandSuggests(t *testing.T) {
	newTestHome(t)
	code, _, errOut := runCLI(t, "taj")
	if code != 2 {
		t.Fatalf("未知命令 exit = %d，应为 2", code)
	}
	if !strings.Contains(errOut, "traj") {
		t.Fatalf("应包含对 traj 的建议: %q", errOut)
	}
}

// TestRunHelpShowsExitCodeNote：run 的帮助必须说明退出码 3 的含义
// ——它是运行的真实结局，不是内部错误。
func TestRunHelpShowsExitCodeNote(t *testing.T) {
	newTestHome(t)
	var out, errBuf bytes.Buffer
	Execute(context.Background(), []string{"run", "--help"}, &out, &errBuf)
	if !strings.Contains(out.String()+errBuf.String(), "退出码") {
		t.Fatal("run 帮助应说明退出码语义")
	}
}
