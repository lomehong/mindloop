package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
)

// TestMemReviseAndInvalidateCLI：CLI 修订/失效入口闭环——add 输出的
// id 能用于 revise；旧 id 退出检索、新 id 生效；invalidate 保留文件
// 但退出检索；list 标注状态。
func TestMemReviseAndInvalidateCLI(t *testing.T) {
	newTestHome(t)
	if code, _, errOut := runCLI(t, "identity", "create", "ada"); code != 0 {
		t.Fatalf("identity create: %d %s", code, errOut)
	}
	code, out, errOut := runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "操作员住在杭州")
	if code != 0 {
		t.Fatalf("mem add: %d %s", code, errOut)
	}
	oldID := strings.Fields(out)[0]

	code, out, errOut = runCLI(t, "mem", "revise", "--identity", "ada", oldID, "操作员搬到了上海")
	if code != 0 {
		t.Fatalf("mem revise: %d %s", code, errOut)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 || fields[0] == oldID {
		t.Fatalf("revise 输出首个字段应为新 id: %q", out)
	}
	newID := fields[0]
	if !strings.Contains(out, "替代") || !strings.Contains(out, oldID) {
		t.Fatalf("revise 输出应说明替代关系: %q", out)
	}

	// 检索：新内容命中新 id，旧内容不再命中。
	code, out, _ = runCLI(t, "mem", "search", "--identity", "ada", "上海")
	if code != 0 || !strings.Contains(out, newID) {
		t.Fatalf("检索新版本失败: %d %q", code, out)
	}
	code, out, _ = runCLI(t, "mem", "search", "--identity", "ada", "杭州")
	if strings.Contains(out, oldID) {
		t.Fatalf("旧版本不应被检索: %q", out)
	}

	// 列表标注被替代状态。
	code, out, _ = runCLI(t, "mem", "list", "--identity", "ada")
	if code != 0 || !strings.Contains(out, "[superseded]") {
		t.Fatalf("list 应标注 superseded: %d %q", code, out)
	}

	// 失效新条：退出检索、文件保留（list 仍可见）。
	code, out, errOut = runCLI(t, "mem", "invalidate", "--identity", "ada", newID)
	if code != 0 || !strings.Contains(out, newID) {
		t.Fatalf("mem invalidate: %d %q %q", code, out, errOut)
	}
	code, out, _ = runCLI(t, "mem", "search", "--identity", "ada", "上海")
	if strings.Contains(out, newID) {
		t.Fatalf("失效后不应被检索: %q", out)
	}
	code, out, _ = runCLI(t, "mem", "list", "--identity", "ada")
	if !strings.Contains(out, "[invalid]") {
		t.Fatalf("list 应标注 invalid: %q", out)
	}

	// 重复失效与未知 id 报错（非零退出码）。
	if code, _, _ = runCLI(t, "mem", "invalidate", "--identity", "ada", newID); code == 0 {
		t.Fatal("重复失效应报错")
	}
}

// TestMemAddDedupAndConflictCLI：add 的重复命中不重复写盘并提示；
// 高相似内容在 stderr 提示冲突候选与显式修订入口（stdout 的 id
// 输出不被污染，agent 仍可机器读取）。
func TestMemAddDedupAndConflictCLI(t *testing.T) {
	newTestHome(t)
	if code, _, errOut := runCLI(t, "identity", "create", "ada"); code != 0 {
		t.Fatalf("identity create: %d %s", code, errOut)
	}
	code, out, errOut := runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "操作员张伟住在杭州")
	if code != 0 {
		t.Fatalf("mem add: %d %s", code, errOut)
	}
	id1 := strings.Fields(out)[0]

	// 完全相同：不重复写盘，提示已存在。
	code, out, errOut = runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "操作员张伟住在杭州")
	if code != 0 {
		t.Fatalf("重复 add 失败: %d %s", code, errOut)
	}
	if !strings.Contains(out, "已存在") || !strings.Contains(out, id1) {
		t.Fatalf("重复写入应提示已存在: %q", out)
	}
	if code, out, _ = runCLI(t, "mem", "list", "--identity", "ada", "-n", "0"); strings.Count(out, "fact") != 1 {
		t.Fatalf("库内应只有 1 条: %q", out)
	}

	// 高相似：stderr 提示冲突候选与 revise 入口。
	code, out, errOut = runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "操作员张伟住在上海")
	if code != 0 {
		t.Fatalf("mem add: %d %s", code, errOut)
	}
	if !strings.Contains(errOut, "相似") || !strings.Contains(errOut, id1) || !strings.Contains(errOut, "revise") {
		t.Fatalf("冲突提示不完整: %q", errOut)
	}
	if strings.Contains(out, "相似") {
		t.Fatalf("提示不应污染 stdout 的 id 输出: %q", out)
	}
}

// TestMCPServeMemTools：MCP 记忆工具面——mem_add 支持来源步骤并在
// 重复/冲突时给出明确提示；mem_search 结果携带记忆 ID 与来源。
func TestMCPServeMemTools(t *testing.T) {
	newTestHome(t)
	ctx := context.Background()
	if _, err := identity.Create(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	var add, search *mcp.ServerTool
	tools := mcpServeTools("ada")
	for i := range tools {
		switch tools[i].Name {
		case "mem_add":
			add = &tools[i]
		case "mem_search":
			search = &tools[i]
		}
	}
	if add == nil || search == nil {
		t.Fatal("缺少 mem_add/mem_search 工具")
	}

	out, err := add.Handler(ctx, json.RawMessage(`{"content":"操作员张伟住在杭州","type":"fact","source":"msg00000001"}`))
	if err != nil || !strings.Contains(out, "已写入") {
		t.Fatalf("mem_add 失败: %q, %v", out, err)
	}

	out, err = add.Handler(ctx, json.RawMessage(`{"content":"操作员张伟住在上海"}`))
	if err != nil || !strings.Contains(out, "相似") {
		t.Fatalf("冲突应提示: %q, %v", out, err)
	}

	out, err = add.Handler(ctx, json.RawMessage(`{"content":"操作员张伟住在杭州"}`))
	if err != nil || !strings.Contains(out, "已存在") {
		t.Fatalf("重复应提示: %q, %v", out, err)
	}

	out, err = search.Handler(ctx, json.RawMessage(`{"query":"杭州","k":3}`))
	if err != nil || !strings.Contains(out, "msg00000001") {
		t.Fatalf("mem_search 应携带来源: %q, %v", out, err)
	}
}
