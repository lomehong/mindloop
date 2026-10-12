package runner

// 完成线机械验收（VerifyDeliverables）的确定性验证：显式委托的 FINAL
// 必须是结构化契约且交付文件真实存在——缺件打回下一轮，验收标准从
// "写给模型看的建议"变成机械判定（真实事故：模型把勘探里程碑当终局，
// 任务书完成线两条都不满足却标 succeeded）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/traj"
)

// finalJSON 是合规收口契约（围栏内 bash 写哨兵文件）。
func finalJSON(summary string, deliverables []string) string {
	items := make([]string, 0, len(deliverables))
	for _, d := range deliverables {
		items = append(items, `"`+d+`"`)
	}
	return fence(`printf '%s' '` + `{"summary":"` + summary + `","deliverables":[` + strings.Join(items, ",") + `]}' > .mindloop_final`)
}

func TestFinalDeliverableVerification(t *testing.T) {
	requireBash(t)
	t.Run("交付物齐全则采信并回传绝对路径", func(t *testing.T) {
		tl := newTestTimeline(t)
		thinker := &fakeThinker{responses: []string{
			fence("echo 正文 > report.md"),
			finalJSON("报告已交付", []string{"report.md"}),
		}}
		res, err := Run(context.Background(), Options{
			Timeline: tl, Thinker: thinker, Task: "写报告", TaskID: "t1", Attempt: 1, RunID: "run-dlv-ok",
			VerifyDeliverables: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Final != "报告已交付" {
			t.Fatalf("Final 应为契约 summary，得到 %q", res.Final)
		}
		if len(res.Deliverables) != 1 || filepath.Base(res.Deliverables[0]) != "report.md" {
			t.Fatalf("Deliverables 应含 report.md 绝对路径: %v", res.Deliverables)
		}
		if !filepath.IsAbs(res.Deliverables[0]) {
			t.Fatalf("交付物应为绝对路径: %q", res.Deliverables[0])
		}
	})

	t.Run("缺件打回下一轮，补齐后才收口", func(t *testing.T) {
		tl := newTestTimeline(t)
		thinker := &fakeThinker{responses: []string{
			finalJSON(" premature", []string{"missing.md"}),
			fence("echo 补齐 > missing.md"),
			finalJSON("这回真的交付了", []string{"missing.md"}),
		}}
		res, err := Run(context.Background(), Options{
			Timeline: tl, Thinker: thinker, Task: "写报告", TaskID: "t2", Attempt: 1, RunID: "run-dlv-miss",
			VerifyDeliverables: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Final != "这回真的交付了" {
			t.Fatalf("打回后应继续到真正收口，Final=%q", res.Final)
		}
		if thinker.calls != 3 {
			t.Fatalf("应消耗 3 轮（拒绝不终止循环），实际 %d", thinker.calls)
		}
		// 拒绝原因落轨迹，模型下轮可见。
		steps, err := tl.Steps()
		if err != nil {
			t.Fatal(err)
		}
		rejected := false
		for _, s := range steps {
			if s.Type != traj.TypeError {
				continue
			}
			if preview, _ := s.Field("content"); strings.Contains(preview, "完成线未满足") {
				rejected = true
			}
		}
		if !rejected {
			t.Fatal("拒绝原因应作为 error 步骤落轨迹")
		}
	})

	t.Run("裸文本 FINAL 不符合契约同样打回", func(t *testing.T) {
		tl := newTestTimeline(t)
		thinker := &fakeThinker{responses: []string{
			`FINAL="我觉得做完了"`,
			finalJSON("合规收口", nil),
		}}
		res, err := Run(context.Background(), Options{
			Timeline: tl, Thinker: thinker, Task: "问答任务", TaskID: "t3", Attempt: 1, RunID: "run-dlv-bare",
			VerifyDeliverables: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Final != "合规收口" || res.Deliverables != nil {
			t.Fatalf("裸 FINAL 应被打回后按契约收口: %+v", res)
		}
	})

	t.Run("越出工作目录的交付物被拒", func(t *testing.T) {
		tl := newTestTimeline(t)
		thinker := &fakeThinker{responses: []string{
			finalJSON("越界", []string{"../../escape.txt"}),
		}}
		_, err := Run(context.Background(), Options{
			Timeline: tl, Thinker: thinker, Task: "写报告", TaskID: "t4", Attempt: 1, RunID: "run-dlv-esc",
			VerifyDeliverables: true, MaxIterations: 1,
		})
		if !errors.Is(err, ErrMaxIterations) {
			t.Fatalf("唯一一轮被拒绝后应轮次耗尽，得到: %v", err)
		}
	})

	t.Run("验收关闭时保持旧语义", func(t *testing.T) {
		tl := newTestTimeline(t)
		thinker := &fakeThinker{responses: []string{
			`FINAL="没有交付物的自主结论"`,
		}}
		res, err := Run(context.Background(), Options{
			Timeline: tl, Thinker: thinker, Task: "自主思考", RunID: "run-dlv-off",
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Final != "没有交付物的自主结论" || res.Deliverables != nil {
			t.Fatalf("非验收模式应保持 FINAL 直采语义: %+v", res)
		}
	})

	t.Run("空文件不算交付", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "empty.md"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		r := &run{opts: Options{VerifyDeliverables: true}, workDir: dir}
		missing, resolved := r.verifyDeliverables([]string{"empty.md"})
		if len(missing) != 1 || resolved != nil {
			t.Fatalf("空文件应判缺失: missing=%v resolved=%v", missing, resolved)
		}
	})
}
