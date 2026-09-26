package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// TestExtractBareFinalDeclaration 表驱动钉死裸文本 FINAL= 声明的
// 识别边界（实测事故轨迹 5518efef：模型完成后把 FINAL 写成裸文本，
// 旧路径整段当命令执行 → 报错 → 轮次耗尽 → 成果吞没）。
func TestExtractBareFinalDeclaration(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantFinal    string // 期望的 Final（空 = 不按完成语义）
		wantCode     string // 期望的执行代码（Final 为空时检查）
		wantTeaching bool   // 期望进入教学纠偏（Notice 指回代码块格式）
	}{
		{
			name:      "裸 FINAL 双引号",
			text:      `FINAL="评审完成，共 12 项"`,
			wantFinal: "评审完成，共 12 项",
		},
		{
			name:      "裸 FINAL 单引号",
			text:      `FINAL='done'`,
			wantFinal: "done",
		},
		{
			name:      "裸 FINAL 无引号",
			text:      "FINAL=直接完成",
			wantFinal: "直接完成",
		},
		{
			// 前置正文只认 FINAL：成果叙述不进终局，也不执行。
			name:      "前置正文只认 FINAL 行",
			text:      "评审已经完成。\n所有发现已写入记忆。\nFINAL=\"评审完成：12 个文件，0 个阻塞问题\"",
			wantFinal: "评审完成：12 个文件，0 个阻塞问题",
		},
		{
			name:      "行首缩进不阻碍识别",
			text:      "  \tFINAL=\"缩进也算\"",
			wantFinal: "缩进也算",
		},
		{
			// 空值不构成完成声明：维持教学纠偏。
			name:         "空 FINAL 值不算声明",
			text:         "FINAL=",
			wantCode:     "FINAL=",
			wantTeaching: true,
		},
		{
			// 无块无 FINAL：教学纠偏路径原样保留。
			name:         "无块无 FINAL 维持教学纠偏",
			text:         "echo 我忘了写代码块",
			wantCode:     "echo 我忘了写代码块",
			wantTeaching: true,
		},
		{
			// 有 bash 块时正规路径永远优先；块外裸 FINAL 不参与终局，
			// 但会被教学纠偏（详见 TestExtractOutsideFinalTeaching）。
			name:         "有块时走正规路径，块外 FINAL 进教学",
			text:         "```bash\necho hi\nFINAL=\"from block\"\n```\nFINAL=\"bare\"",
			wantCode:     "echo hi\nFINAL=\"from block\"",
			wantTeaching: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := extractCode(tt.text)
			if ext.Final != tt.wantFinal {
				t.Fatalf("Final = %q，期望 %q", ext.Final, tt.wantFinal)
			}
			if tt.wantTeaching {
				if !strings.Contains(ext.Notice, "代码块") {
					t.Fatalf("应进入教学纠偏（提示指回代码块格式），Notice = %q", ext.Notice)
				}
				if ext.Code != tt.wantCode {
					t.Fatalf("Code = %q，期望 %q", ext.Code, tt.wantCode)
				}
				return
			}
			if ext.Notice != "" {
				t.Fatalf("不应有教学提示，Notice = %q", ext.Notice)
			}
			if tt.wantFinal == "" && ext.Code != tt.wantCode {
				t.Fatalf("Code = %q，期望 %q", ext.Code, tt.wantCode)
			}
		})
	}
}

// 裸 FINAL 声明在运行循环里的成功语义：正常结束、final 步骤落盘、
// 不进沙箱（无 shell-output/error 步骤）、不教学回灌。此路径不
// 依赖 bash——恰是"模型已完成、执行面已无关"的语义。
func TestRunBareFinalDeclarationCompletes(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantFinal string
	}{
		{
			name:      "裸 FINAL 行",
			response:  `FINAL="评审完成"`,
			wantFinal: "评审完成",
		},
		{
			name:      "前置正文 + 裸 FINAL 行只认 FINAL",
			response:  "评审已完成，成果已写盘。\nFINAL=\"评审完成：0 阻塞\"",
			wantFinal: "评审完成：0 阻塞",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl := newTestTimeline(t)
			thinker := &fakeThinker{responses: []string{tt.response}}
			res, err := Run(context.Background(), Options{
				Timeline: tl,
				Thinker:  thinker,
				Task:     "裸文本终局声明",
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Final != tt.wantFinal || res.Iterations != 1 {
				t.Fatalf("final=%q iterations=%d", res.Final, res.Iterations)
			}
			steps, err := tl.Steps()
			if err != nil {
				t.Fatal(err)
			}
			var types []string
			for _, s := range steps {
				types = append(types, s.Type)
			}
			// 不应有 shell-output（什么都没执行）与 error（没有失败）。
			if strings.Join(types, ",") != "trajectory,run,prompt,reasoning,final" {
				t.Fatalf("步骤序列 = %v", types)
			}
			finalStep := steps[len(steps)-1]
			if finalStep.Type != traj.TypeFinal {
				t.Fatalf("最后一步应为 final: %s", finalStep.Type)
			}
			if c, _ := finalStep.Field("content"); c != tt.wantFinal {
				t.Fatalf("final 步骤内容 = %q", c)
			}
		})
	}
}

// 有 bash 块的正常声明路径不受影响：块内 FINAL 经沙箱哨兵生效，
// 块外裸 FINAL 行即使同时出现也不改写结果。
func TestRunBlockFinalWinsOverBareLine(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		"```bash\necho hi\nFINAL=\"from block\"\n```\nFINAL=\"bare line\"",
	}}
	res, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "正规块声明优先",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Final != "from block" {
		t.Fatalf("正规块内声明应生效，final = %q", res.Final)
	}
	steps, _ := tl.Steps()
	hasShellOutput := false
	for _, s := range steps {
		if s.Type == "shell-output" {
			hasShellOutput = true
		}
	}
	if !hasShellOutput {
		t.Fatal("正规路径应有 shell-output 步骤")
	}
}

// 回归护栏：无块、无 FINAL、且整段作为命令必然失败（非命令文本）
// ——必须走执行→报错→运行失败的老路，绝不能被误判为终局成功。
func TestRunBareFinalDoesNotSwallowFailure(t *testing.T) {
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		"这不是命令，也不是 FINAL 声明。",
	}}
	_, err := Run(context.Background(), Options{
		Timeline:      tl,
		Thinker:       thinker,
		Task:          "失败文本不冒充终局",
		MaxIterations: 1,
	})
	if err == nil {
		t.Fatal("失败文本不应被当成终局成功")
	}
}

// TestExtractOutsideFinalTeaching 钉死"块外 FINAL= 声明"的教学纠偏
// 边界（实测事故 mind 轨迹 5518efef：模型每轮把 FINAL= 写在代码块外，
// 系统静默忽略 → 收不了尾 → 轮次耗尽连环循环）。
func TestExtractOutsideFinalTeaching(t *testing.T) {
	block := fence("true")
	tests := []struct {
		name       string
		text       string
		wantCode   string
		wantNotice []string // 期望 Notice 包含的片段（nil = 期望无 Notice）
	}{
		{
			name:       "块后块外 FINAL",
			text:       block + "\nFINAL=\"IDLE——无新信息无欠账\"",
			wantCode:   "true",
			wantNotice: []string{"FINAL", "代码块外"},
		},
		{
			name:       "块前块外 FINAL",
			text:       "FINAL=\"先声明后干活\"\n" + block,
			wantCode:   "true",
			wantNotice: []string{"FINAL", "代码块外"},
		},
		{
			// 块内声明是正规路径，绝不教学。
			name:       "块内 FINAL 不触发",
			text:       fence("echo hi\nFINAL=\"done\""),
			wantCode:   "echo hi\nFINAL=\"done\"",
			wantNotice: nil,
		},
		{
			// heredoc 正文里的 FINAL= 不是声明。
			name:       "heredoc 正文不触发",
			text:       fence("cat <<'EOF'\nFINAL=\"不是声明\"\nEOF"),
			wantCode:   "cat <<'EOF'\nFINAL=\"不是声明\"\nEOF",
			wantNotice: nil,
		},
		{
			// 多个块 + 块外 FINAL：两条教学都回灌。
			name:       "多块与块外 FINAL 叠加",
			text:       block + "\n" + fence("FINAL=\"second block\""),
			wantCode:   "true",
			wantNotice: []string{"多个代码块", "FINAL"},
		},
		{
			// 空值块外声明同样是位置错误，要教学。
			name:       "块外空值也教学",
			text:       block + "\nFINAL=",
			wantCode:   "true",
			wantNotice: []string{"FINAL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := extractCode(tt.text)
			if ext.Final != "" {
				t.Fatalf("块外 FINAL 不得被采为终局，Final = %q", ext.Final)
			}
			if ext.Code != tt.wantCode {
				t.Fatalf("Code = %q，期望 %q", ext.Code, tt.wantCode)
			}
			if len(tt.wantNotice) == 0 {
				if ext.Notice != "" {
					t.Fatalf("不应有教学提示，Notice = %q", ext.Notice)
				}
				return
			}
			for _, want := range tt.wantNotice {
				if !strings.Contains(ext.Notice, want) {
					t.Fatalf("Notice = %q，应包含 %q", ext.Notice, want)
				}
			}
		})
	}
}
