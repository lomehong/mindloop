// subtraj.go：子轨迹服务——Super-agent 双层架构（2026-11-05）的
// viewer 可见性补全。并发任务的 Worker 在 fork 子轨迹里执行
// （internal/mind/tasks.go 的 runWorker），根轨迹只留 mandate/fork/
// merge 控制面叙事；本文件把 fork 的内容接进 web 面板：
//
//	/traj/{id}      子轨迹的完整 mindlog（步骤 + run 卡 + 面包屑）
//	/traj/{id}/blob fork 目录下保留的大输出
//	/runs/{runId}   run 的 prompt 全文（根未命中时扫 fork）
//	/step/{stepId}  步骤详情（同上）
//
// id 的两种形态都接受：轨迹 UUID（或唯一前缀——traj.LoadAt 的原生
// 语义）与磁盘目录名（"<id8>-<slug>"，tree 侧栏与排查现场使用的
// 形态）。解析永远不出身份的 trajectories/ 根——id 是经根目录解析
// 的名字，不是文件系统路径。
package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// identityTrajRoot 返回身份的轨迹根目录（根轨迹与全部 fork 的共同
// 父目录）——fork 落盘的位置（runWorker 的 filepath.Dir(root.Dir)）。
func identityTrajRoot(id *identity.Identity) string {
	return filepath.Dir(id.Timeline.Dir)
}

// resolveIdentityTraj 在身份轨迹根内把 ref（UUID/前缀 或 目录名）
// 解析成轨迹句柄。找不到返回 traj.ErrNotFound（调用方转 404）；
// 含路径分隔符/.. 的 ref 在目录名分支前就拒绝——LoadAt 分支按
// UUID 前缀匹配天然忽略它们，但显式拒绝让语义更直白。
func (s *Server) resolveIdentityTraj(id *identity.Identity, ref string) (*traj.Timeline, error) {
	if ref == "" {
		return nil, traj.ErrNotFound
	}
	root := identityTrajRoot(id)
	// 1) UUID / 唯一前缀：LoadAt 只在 root 内扫 header，id 不当路径用。
	if tl, err := traj.LoadAt(root, ref); err == nil {
		return tl, nil
	} else if errors.Is(err, traj.ErrAmbiguous) {
		return nil, err
	}
	// 2) 目录名精确匹配（"<id8>-<slug>" 形态，tree 侧栏上报的 id）。
	if strings.ContainsAny(ref, `/\`) || strings.Contains(ref, "..") {
		return nil, traj.ErrNotFound
	}
	dir := filepath.Join(root, ref)
	if rel, err := filepath.Rel(root, dir); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, traj.ErrNotFound
	}
	return loadTimelineAt(dir)
}

// loadTimelineAt 在目录内构造轨迹句柄：header 必须可读（不可读=
// 不是轨迹），并回填 ID/Slug（LoadAt 的同款行为——手工构造的
// Timeline 只填了 Dir/Path）。
func loadTimelineAt(dir string) (*traj.Timeline, error) {
	tl := &traj.Timeline{Dir: dir, Path: filepath.Join(dir, "trajectory.jsonl")}
	h, err := tl.Header()
	if err != nil {
		return nil, traj.ErrNotFound
	}
	tl.ID = h.StepID
	if v, ok := h.Field("slug"); ok {
		tl.Slug = v
	}
	return tl, nil
}

// identityChildTrajectories 枚举根轨迹引用的全部直接子轨迹（fork
// 步骤的 child_ref），返回可读的轨迹句柄。child_ref 相对**父轨迹
// 目录**（createAt 的 rel(parent.Dir, t.Dir)，形如 "../<dir>"），所以
// Join 的基点是 id.Timeline.Dir；包含校验的基点是**身份轨迹根**
// ——解析结果必须落在根内，越界引用（child_ref 来自日志内容，
// traj append --field 可任意写入）直接跳过，绝不 Stat/读文件。
// （原 childTrajectories 以父轨迹目录做校验基点，合法的 "../<dir>"
// 形态被全数误杀——tree 侧栏从未显示过 fork，此为修正。）
func (s *Server) identityChildTrajectories(id *identity.Identity) []*traj.Timeline {
	ix, err := s.indexes.get(id.Timeline)
	if err != nil {
		return nil
	}
	refs, err := ix.ForkRefs()
	if err != nil {
		return nil
	}
	trajRoot := identityTrajRoot(id)
	seen := map[string]bool{}
	var out []*traj.Timeline
	for _, ref := range refs {
		childPath := filepath.Join(id.Timeline.Dir, ref)
		if rel, rerr := filepath.Rel(trajRoot, childPath); rerr != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		// child_ref 的两种历史形态：traj.ForkAt 写的是**目录**相对
		// 路径（rel(parent.Dir, t.Dir)）；手写 append（旧测试/人工
		// 排查）曾带 trajectory.jsonl 文件名。统一剥成目录。
		childDir := childPath
		if filepath.Base(childPath) == "trajectory.jsonl" {
			childDir = filepath.Dir(childPath)
		}
		if seen[childDir] {
			continue
		}
		tl, err := loadTimelineAt(childDir)
		if err != nil {
			continue // 不可读的目录不是子轨迹
		}
		seen[childDir] = true
		out = append(out, tl)
	}
	return out
}

// trajBreadcrumb 沿 parent_traj 链向上收集 {traj_id, slug}，反转成
// [root, …, 当前]。父链的解析限在身份轨迹根内（LoadAt）；断链
// （父不可读）就地停住——面包屑是导航增强，不是完整性断言。环由
// seen 防护（traj.Root 的同一纪律）。
func trajBreadcrumb(id *identity.Identity, tl *traj.Timeline) []subCrumb {
	root := identityTrajRoot(id)
	seen := map[string]bool{tl.ID: true}
	var chain []subCrumb
	cur := tl
	for {
		slug := cur.Slug
		if slug == "" {
			if h, err := cur.Header(); err == nil {
				if v, ok := h.Field("slug"); ok {
					slug = v
				}
			}
		}
		chain = append(chain, subCrumb{TrajID: cur.ID, Slug: slug})
		h, err := cur.Header()
		if err != nil {
			break
		}
		pid, _ := h.Field("parent_traj")
		if pid == "" || seen[pid] {
			break
		}
		seen[pid] = true
		parent, err := traj.LoadAt(root, pid)
		if err != nil {
			break
		}
		cur = parent
	}
	// 反转：链是 当前→root，面包屑要 root→当前。
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// handleSubTrajectory 返回（子）轨迹的完整 mindlog——viewer 子轨迹
// 页契约。fork 是单任务量级的文件，全量返回不做 since 窗口（页面
// 本来就 2s 全量轮询）。根轨迹本身也可经此端点服务（统一入口，
// 面包屑退化为 [root]）。
func (s *Server) handleSubTrajectory(w http.ResponseWriter, _ *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		writeError(w, 400, "缺少轨迹 id")
		return
	}
	tl, err := s.resolveIdentityTraj(id, rest[0])
	if err != nil {
		writeError(w, 404, "轨迹不存在: "+rest[0])
		return
	}
	steps, err := tl.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	norm := make([]normalizedStep, len(steps))
	for i, st := range steps {
		norm[i] = normalizeStep(st)
	}
	view := mindlogNormalized{
		TrajID:    tl.ID,
		DirRel:    relTrajDir(s.cfg.Root, tl.Dir),
		StepCount: len(norm),
		Steps:     norm,
		Runs:      groupRuns(steps, norm),
		// 运行锁是身份级的：Worker 在跑 = 心智在跑，live 对 fork
		// 同样成立（页面据此决定轮询节奏）。
		Live:     isIdentityLive(id.Timeline.Dir),
		Identity: identityRef{ID: id.Name, Name: id.Name},
	}
	out := subTrajectory{Mindlog: view, Breadcrumb: trajBreadcrumb(id, tl)}
	// parent 链接：当前轨迹的头携带 parent_traj/parent_step（fork
	// 双向引用的回程），有才出现。
	if h, err := tl.Header(); err == nil {
		if pid, _ := h.Field("parent_traj"); pid != "" {
			ref := subParentRef{TrajID: pid}
			if ps, ok := h.Field("parent_step"); ok && ps != "" {
				ref.StepID = &ps
			}
			out.Parent = &ref
		}
	}
	writeJSON(w, 200, out)
}
