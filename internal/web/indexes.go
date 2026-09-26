package web

// 共享侧索引：每个身份（键：Timeline.Path）在 web 进程内共享一个
// traj.Index——status/chat/activity/tree/thinkers 等高频端点共用同
// 一份增量观察，日志仍是唯一事实源（外部追加/替换由 Index 的追平
// 与重建覆盖，共享不牺牲新鲜度）。
//
// 生命周期注记：当前随进程常驻；D3 的共享文件观察器接管后由观察者
// 持有同一实例，无订阅者时释放。

import (
	"sync"

	"mindloop/internal/traj"
)

type indexStore struct {
	mu     sync.Mutex
	byPath map[string]*traj.Index
}

func newIndexStore() *indexStore {
	return &indexStore{byPath: map[string]*traj.Index{}}
}

// get 返回 tl 的共享侧索引；同一路径在进程内只有一个实例。
func (s *indexStore) get(tl *traj.Timeline) (*traj.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ix, ok := s.byPath[tl.Path]; ok {
		return ix, nil
	}
	ix, err := traj.OpenIndex(tl)
	if err != nil {
		return nil, err
	}
	s.byPath[tl.Path] = ix
	return ix, nil
}
