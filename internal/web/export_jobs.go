package web

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/ids"
)

// exportJob 是单身份导出任务的内存态（viewer ExportJob 契约）。
// 产物文件落在系统临时目录，DELETE 时删除、进程退出由系统回收——
// 导出是可再生数据，不值得为它做持久化状态机。
type exportJob struct {
	ID        string
	Identity  string
	Status    string // running | done | failed
	StartedAt string
	SoulOnly  bool
	Slim      bool
	Seconds   float64
	Size      int64
	Filename  string
	Err       string
	path      string // 产物文件路径，不进 JSON
}

// exportJobsMu 保护 jobs 注册表与每个 job 的全部字段——后台写
// goroutine 与 HTTP 读路径只经它见面，不搞细粒度锁。
var (
	exportJobsMu sync.Mutex
	exportJobs   = map[string]*exportJob{}
)

// jobView 把 job 快照成 viewer ExportJob 契约（调用方持锁前复制，
// 避免把锁带到 JSON 编码里）。
func jobView(j *exportJob) map[string]any {
	exportJobsMu.Lock()
	defer exportJobsMu.Unlock()
	return j.snapshot()
}

func (j *exportJob) snapshot() map[string]any {
	size, filename, errMsg := any(nil), any(nil), any(nil)
	if j.Status == "done" && j.Size > 0 {
		size = j.Size
	}
	if j.Filename != "" {
		filename = j.Filename
	}
	if j.Err != "" {
		errMsg = j.Err
	}
	return map[string]any{
		"job_id":       j.ID,
		"identity_id":  j.Identity,
		"status":       j.Status,
		"started_at":   j.StartedAt,
		"soul_only":    j.SoulOnly,
		"slim":         j.Slim,
		"seconds":      j.Seconds,
		"size":         size,
		"filename":     filename,
		"error":        errMsg,
		"download_url": "/api/export-jobs/" + j.ID + "/download",
	}
}

// handleExportJobCreate POST /api/identities/{id}/export-jobs
// {soul_only, slim} → ExportJob（初始 running，前端轮询到 done）。
func (s *Server) handleExportJobCreate(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		SoulOnly bool `json:"soul_only"`
		Slim     bool `json:"slim"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {soul_only, slim} JSON")
		return
	}
	job := &exportJob{
		ID:        ids.Short(ids.NewUUID(), 12),
		Identity:  id.Name,
		Status:    "running",
		StartedAt: nowISO(),
		SoulOnly:  req.SoulOnly,
		Slim:      req.Slim,
	}
	f, err := os.CreateTemp("", "mindloop-export-*.tgz")
	if err != nil {
		writeError(w, 500, "创建导出文件失败: "+err.Error())
		return
	}
	exportJobsMu.Lock()
	exportJobs[job.ID] = job
	exportJobsMu.Unlock()

	go func() {
		start := time.Now()
		err := writeIdentityArchive(f, id, job.SoulOnly, job.Slim)
		fi, statErr := f.Stat()
		f.Close()
		exportJobsMu.Lock()
		defer exportJobsMu.Unlock()
		if err != nil {
			job.Status = "failed"
			job.Err = err.Error()
			job.Seconds = time.Since(start).Seconds()
			os.Remove(f.Name())
			return
		}
		job.Status = "done"
		job.Seconds = time.Since(start).Seconds()
		if statErr == nil {
			job.Size = fi.Size()
		}
		job.Filename = fmt.Sprintf("mindloop-%s-%s.tgz", id.Name, time.Now().UTC().Format("20060102-150405"))
		job.path = f.Name()
	}()
	writeJSON(w, 200, jobView(job))
}

// handleExportJobsList GET /api/identities/{id}/export-jobs → 该身份
// 的任务列表（新任务在前）。
func (s *Server) handleExportJobsList(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	exportJobsMu.Lock()
	var mine []*exportJob
	for _, j := range exportJobs {
		if j.Identity == id.Name {
			mine = append(mine, j)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].StartedAt > mine[j].StartedAt })
	out := make([]map[string]any, 0, len(mine))
	for _, j := range mine {
		out = append(out, j.snapshot())
	}
	exportJobsMu.Unlock()
	writeJSON(w, 200, out)
}

// findExportJob 按 id 取任务快照；不存在返回 nil。
func findExportJob(jobID string) map[string]any {
	exportJobsMu.Lock()
	defer exportJobsMu.Unlock()
	j, ok := exportJobs[jobID]
	if !ok {
		return nil
	}
	return j.snapshot()
}

// handleExportJobGet GET /api/export-jobs/{jobID}。
func (s *Server) handleExportJobGet(w http.ResponseWriter, r *http.Request) {
	view := findExportJob(r.PathValue("jobID"))
	if view == nil {
		writeError(w, 404, "导出任务不存在")
		return
	}
	writeJSON(w, 200, view)
}

// handleExportJobDelete DELETE /api/export-jobs/{jobID}——删任务记录
// 与产物文件；进行中的任务不中断（完成后文件即孤儿，由下次清理
// 或进程退出回收）。
func (s *Server) handleExportJobDelete(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobID")
	exportJobsMu.Lock()
	defer exportJobsMu.Unlock()
	j, ok := exportJobs[jobID]
	if !ok {
		writeError(w, 404, "导出任务不存在")
		return
	}
	if j.path != "" {
		_ = os.Remove(j.path)
	}
	delete(exportJobs, jobID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleExportJobDownload GET /api/export-jobs/{jobID}/download。
func (s *Server) handleExportJobDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobID")
	exportJobsMu.Lock()
	j, ok := exportJobs[jobID]
	path, filename, status := j.path, j.Filename, j.Status
	exportJobsMu.Unlock()
	if !ok {
		writeError(w, 404, "导出任务不存在")
		return
	}
	if status != "done" || path == "" {
		writeError(w, 409, "任务尚未完成（status="+status+"）")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 500, "读取导出文件失败: "+err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	_, _ = io.Copy(w, f)
}

// handleIdentityExport GET /api/identities/{id}/export[?soul_only=true]
// ——同步流式下载单身份归档（api.ts 的 exportIdentityUrl 直链）。
func (s *Server) handleIdentityExport(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	soulOnly := r.URL.Query().Get("soul_only") == "true"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="mindloop-%s-%s.tgz"`, id.Name, time.Now().UTC().Format("20060102-150405")))
	// 头部已发出后失败只能半截归档（gunzip 会失败）——这是流式
	// 下载的诚实上限。
	_ = writeIdentityArchive(w, id, soulOnly, false)
}

// archiveExcluded 报告归档相对路径是否属于永不导出的条目：
//   - .env——密钥不进任何可携带归档（单身份导出与全量导出共用同一条
//     铁律；此前"导出全部"漏掉这条规则，把每个身份的 API key 原样
//     打包进了下载产物——归档一旦被共享或备份即密钥泄露）；
//   - run/——运行控制面（运行锁、停机标志、wake 信号）。
//
// rel 必须是 slash 分隔的相对路径；top 是身份目录在 rel 中的层号：
// 单身份导出为 0（rel 形如 ".env"、"run/x"），全量导出为 1（第 0 层
// 是身份名，rel 形如 "ada/.env"、"ada/run/x"）。.env 按任意层匹配
// （保守排除）。
func archiveExcluded(rel string, top int) bool {
	parts := strings.Split(rel, "/")
	if top < len(parts) && parts[top] == "run" {
		return true
	}
	for _, part := range parts {
		if part == ".env" {
			return true
		}
	}
	return false
}

// writeIdentityArchive 把身份目录按选择条件打包为 tar.gz。
//   - run/ 控制面与 .env 永远排除（archiveExcluded，两条导出路径共用）；
//   - slim：排除 runs/ 工作现场；
//   - soul_only：只要 persona.md、identity.txt、memories/。
func writeIdentityArchive(out io.Writer, id *identity.Identity, soulOnly, slim bool) error {
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	defer gz.Close()
	defer tw.Close()

	include := func(rel string, fi os.FileInfo) bool {
		if archiveExcluded(rel, 0) {
			return false
		}
		if rel == "runs" {
			return !slim && !soulOnly
		}
		if soulOnly {
			return rel == "persona.md" || rel == "identity.txt" ||
				rel == "memories" || strings.HasPrefix(rel, "memories/")
		}
		return true
	}

	base := id.Dir
	return filepath.Walk(base, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if !include(rel, fi) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = "identities/" + id.Name + "/" + rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
}
