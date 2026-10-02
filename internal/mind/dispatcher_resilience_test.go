package mind

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRunSurvivesTransientStepErrors：心跳连败退避——轨迹文件被
// 破坏（step 连续失败）期间调度器不退出；文件恢复后照常投递。
// 伴生发现（roadmap §6）的钉子：一次瞬时文件争用不该永久杀死
// 调度器。
func TestRunSurvivesTransientStepErrors(t *testing.T) {
	d, tl := newTestDispatcher(t)
	tk := &recorderThinker{name: "monolith", sub: Subscription{Types: []string{"message"}}}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 破坏：轨迹文件替换成同名目录——每次 ReadNew 都报错。
	original, err := os.ReadFile(tl.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tl.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tl.Path, 0o755); err != nil {
		t.Fatal(err)
	}

	// 吃掉连续多次失败（退避 0.2+0.4+0.6+0.8s ≈ 2s + 多拍 poll）：
	// 期间 ctx 未取消而 Run 未返回——下面的投递成功即为存活的证据。
	time.Sleep(3 * time.Second)

	// 恢复：还原轨迹字节，追加消息，投递照常。
	if err := os.RemoveAll(tl.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tl.Path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	appendStep(t, tl, "message", "")
	waitFor(t, 30*time.Second, func() bool { return tk.wakeCount() >= 1 })
}
