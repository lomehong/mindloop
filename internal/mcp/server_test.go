package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runServe 喂一批 JSON-RPC 行给 serve，按 id 索引全部响应。
func runServe(t *testing.T, tools []ServerTool, lines ...string) map[string]json.RawMessage {
	t.Helper()
	input := strings.Join(lines, "\n") + "\n"
	out := &bytes.Buffer{}
	if err := serve(context.Background(), strings.NewReader(input), out, "mindloop-test", "0.0.0", tools); err != nil {
		t.Fatalf("serve: %v", err)
	}
	res := map[string]json.RawMessage{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var msg struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.ID == nil {
			continue
		}
		if msg.Error != nil {
			res["err:"+itoa(*msg.ID)] = []byte(msg.Error.Message)
			continue
		}
		res[keyFor(*msg.ID)] = msg.Result
	}
	return res
}

func keyFor(id int64) string { return strings.Repeat("k", int(id%97)+int(id))[:0] + itoa(id) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var testTools = []ServerTool{
	{
		Name:        "greet",
		Description: "打招呼",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Who string `json:"who"`
			}
			json.Unmarshal(args, &a)
			if a.Who == "" {
				return "", errGreetEmpty
			}
			return "hello " + a.Who, nil
		},
	},
}

var errGreetEmpty = greetError{}

type greetError struct{}

func (greetError) Error() string { return "who 不能为空" }

func TestServeInitializeToolsListCall(t *testing.T) {
	res := runServe(t, testTools, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"greet","arguments":{"who":"ada"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"greet","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
		`garbage line`,
	}...)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(res[keyFor(1)], &init)
	if init.ProtocolVersion != protocolVersion || init.ServerInfo.Name != "mindloop-test" {
		t.Fatalf("initialize 错位: %+v", init)
	}

	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(res[keyFor(2)], &list)
	if len(list.Tools) != 1 || list.Tools[0].Name != "greet" {
		t.Fatalf("tools/list 错位: %+v", list.Tools)
	}

	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(res[keyFor(3)], &call)
	if call.IsError || call.Content[0].Text != "hello ada" {
		t.Fatalf("greet 错位: %+v", call)
	}

	call = struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}{}
	json.Unmarshal(res[keyFor(4)], &call)
	if !call.IsError || !strings.Contains(call.Content[0].Text, "who 不能为空") {
		t.Fatalf("处理器错误应以 isError 回给客户端: %+v", call)
	}

	if msg, ok := res["err:"+itoa(5)]; !ok || !strings.Contains(string(msg), "未知工具") {
		t.Fatalf("未知工具应 -32602: %v", msg)
	}

	if _, ok := res[keyFor(6)]; !ok {
		t.Fatal("ping 应有空 result")
	}
}

// TestServeStdioFromFile：真实文件流走一遍 serve（与 stdio 进程
// 读取路径同构）。
func TestServeStdioFromFile(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	in := filepath.Join(t.TempDir(), "in.jsonl")
	if err := os.WriteFile(in, []byte(req+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := &bytes.Buffer{}
	if err := serve(context.Background(), f, out, "m", "0", testTools); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tools"`) {
		t.Fatalf("输出缺少 tools: %q", out.String())
	}
}
