package web

import (
	"context"
	"sort"

	"mindloop/internal/mem"
)

// realMemStore 是 memStore 函数的真实实现，适配 memStoreIface。
// 隔离在该文件避免 handlers.go 直接依赖 mem 包引发循环导入风险。
type realMemStore struct {
	dir string
}

func (s *realMemStore) List() ([]memoryRow, error) {
	store := mem.Store{Dir: s.dir}
	all, err := store.List()
	if err != nil {
		return nil, err
	}
	return convertMemory(all), nil
}

func (s *realMemStore) Search(query string, topK int) ([]memoryRow, error) {
	store := mem.Store{Dir: s.dir}
	hits, err := store.Search(query, topK)
	if err != nil {
		return nil, err
	}
	out := make([]memoryRow, 0, len(hits))
	for _, h := range hits {
		out = append(out, memoryRow{
			ID: h.ID, Type: h.Type, Summary: h.Summary,
			Created: h.Created, Path: h.Path, Status: h.Status,
		})
	}
	// BM25 是按评分排的——search 已排好；这里防御性地按 ID 兜底排序
	// 保持输出稳定。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Revise 修订一条记忆并把旧版本标记为被替代（文件保留可追溯），
// 返回新版本 id。
func (s *realMemStore) Revise(ctx context.Context, id, content string) (string, error) {
	added, err := mem.Store{Dir: s.dir}.Revise(ctx, id, content)
	if err != nil {
		return "", err
	}
	return added.Memory.ID, nil
}

// Invalidate 显式失效一条记忆：文件保留（审计），但退出检索。
func (s *realMemStore) Invalidate(ctx context.Context, id string) error {
	return mem.Store{Dir: s.dir}.Invalidate(ctx, id)
}

func convertMemory(in []mem.Memory) []memoryRow {
	out := make([]memoryRow, 0, len(in))
	for _, m := range in {
		out = append(out, memoryRow{
			ID: m.ID, Type: m.Type, Summary: m.Summary,
			Content: m.Content, Created: m.Created, Path: m.Path, Status: m.Status,
		})
	}
	return out
}
