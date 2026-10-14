package mind

// 场景卡片 TTL 清扫（theater.md §16.2）：ambient 卡是"背景陈列"，
// 有保鲜期——超龄未更新即落 remove 步骤（exit_reason=ttl），投影端
// 把它降为单行摘要沉淀在右列底部（不蒸发），隔夜并入账卡。丢与
// 洪峰之外的第三条路。confront 是待决事实、progress 是进行中状态，
// 都由显式生命周期管理，不参与时钟驱逐。

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// ExpiredSceneIDs 折叠 scene 步骤，返回超龄 ambient 卡的 id（按 id
// 排序——同一状态同一顺序）。remove 即出列；同 id 重发刷新时效。
func ExpiredSceneIDs(steps []traj.Step, now time.Time, ttl time.Duration) []string {
	type liveCard struct {
		intent string
		at     time.Time
	}
	live := map[string]liveCard{}
	for _, s := range steps {
		if s.Type != traj.TypeScene {
			continue
		}
		op, _ := s.Field("op")
		id, _ := s.Field("id")
		if id == "" {
			continue
		}
		if op == "remove" {
			delete(live, id)
			continue
		}
		intent, _ := s.Field("intent")
		at, err := time.Parse(time.RFC3339, s.TS)
		if err != nil {
			continue // 时间戳损坏的步骤不参与时钟驱逐
		}
		live[id] = liveCard{intent: intent, at: at}
	}
	ids := make([]string, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		c := live[id]
		if c.intent == "ambient" && now.Sub(c.at) > ttl {
			out = append(out, id)
		}
	}
	return out
}

// SceneTTLSweeper 周期清扫：每分钟折叠一次场景，把超龄 ambient 卡
// 以 remove（exit_reason=ttl）落轨迹，同一分钟的多张共享批号
//（ttl-<unix 分>，投影端单帧落位）。ctx 取消即退出；随心智进程
// 生命周期运行。
func SceneTTLSweeper(ctx context.Context, tl *traj.Timeline, ttl time.Duration, logger func(string, ...any)) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if ttl <= 0 {
				continue
			}
			steps, err := tl.Steps()
			if err != nil {
				continue
			}
			ids := ExpiredSceneIDs(steps, now, ttl)
			if len(ids) == 0 {
				continue
			}
			batch := fmt.Sprintf("ttl-%d", now.Unix()/60)
			failed := 0
			for _, id := range ids {
				err := AppendSceneStep(tl, map[string]any{
					"op": "remove", "id": id,
					"exit_reason": "ttl", "batch_id": batch,
				})
				if err != nil {
					failed++
				}
			}
			if logger != nil && failed == 0 {
				logger("scene ttl 清扫：%d 张过期 ambient 卡沉淀为摘要", len(ids))
			}
		}
	}
}
