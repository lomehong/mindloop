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
	"strings"
	"sync"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// archiveMaxBytes 限制上传体（128MB 足够任何单身份归档）。
const archiveMaxBytes = 128 << 20

// handleExport 把全部身份打包为 tar.gz 下载——首页"导出全部"按钮。
// 内容：identities/ 下每个身份目录（persona/记忆/轨迹/元数据）。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	root := identity.Home()
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="mindloop-identities-%s.tgz"`, time.Now().UTC().Format("20060102-150405")))
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	defer tw.Close()
	defer gz.Close()

	for _, e := range entries {
		if !e.IsDir() || !isSafeIdentName(e.Name()) || isBadIdentSegment(e.Name()) {
			continue
		}
		base := filepath.Join(root, e.Name())
		err := filepath.Walk(base, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, err = io.Copy(tw, f)
				f.Close()
				return err
			}
			return nil
		})
		if err != nil {
			return // 头部已发，半截归档是诚实反馈（gunzip 会失败）
		}
	}
}

// handleImport 从 tar.gz 解包身份——POST /api/identities/import[?name=x]。
// 安全：拒绝绝对路径、..、符号/硬链接条目；体长上限 128MB；
// name 仅当归档恰好含一个身份时生效（重命名导入）。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	root := identity.Home()
	if err := os.MkdirAll(root, 0o755); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	gz, err := gzip.NewReader(http.MaxBytesReader(w, r.Body, archiveMaxBytes))
	if err != nil {
		writeError(w, 400, "不是有效的 gzip 归档: "+err.Error())
		return
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	type fileEntry struct {
		rel  string // "identities/<name>/..."（已去掉前导）
		data []byte
	}
	var files []fileEntry
	topDirs := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, 400, "读取归档失败: "+err.Error())
			return
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			writeError(w, 400, "归档包含符号/硬链接条目，拒绝导入")
			return
		}
		name := filepath.ToSlash(hdr.Name)
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			writeError(w, 400, "归档包含非法路径: "+name)
			return
		}
		trimmed := strings.TrimPrefix(name, "identities/")
		parts := strings.Split(strings.TrimSuffix(trimmed, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		topDirs[parts[0]] = true
		if hdr.Typeflag == tar.TypeDir || hdr.Size == 0 {
			continue
		}
		if hdr.Size > 64<<20 {
			writeError(w, 400, "归档内文件过大: "+name)
			return
		}
		data, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			writeError(w, 400, "读取归档条目失败: "+err.Error())
			return
		}
		files = append(files, fileEntry{rel: trimmed, data: data})
	}
	if len(topDirs) == 0 || len(files) == 0 {
		writeError(w, 400, "归档为空或结构不可识别")
		return
	}

	// name 参数：单身份归档重命名。
	rename := r.URL.Query().Get("name")
	mapping := map[string]string{} // 归档顶名 → 落盘名
	for top := range topDirs {
		target := top
		if rename != "" && len(topDirs) == 1 {
			target = traj.Slugify(rename, 40)
		}
		// 已存在则避让（-unix 后缀）——导入绝不覆盖。
		final := target
		for i := 0; ; i++ {
			if _, err := os.Stat(filepath.Join(root, final)); os.IsNotExist(err) {
				break
			}
			final = fmt.Sprintf("%s-%d", target, int(time.Now().Unix())%10000+i)
		}
		mapping[top] = final
	}

	imported := []map[string]string{}
	for _, f := range files {
		parts := strings.SplitN(strings.TrimPrefix(f.rel, "identities/"), "/", 2)
		if len(parts) != 2 {
			continue
		}
		top, rest := mapping[parts[0]], parts[1]
		dest := filepath.Join(root, top, rest)
		// 双保险：落盘路径必须仍在 root 下。
		if !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
			writeError(w, 400, "非法落盘路径: "+dest)
			return
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if err := os.WriteFile(dest, f.data, 0o644); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	for _, target := range mapping {
		if id, err := identity.Load(target); err == nil {
			imported = append(imported, map[string]string{"id": id.Name, "name": id.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "imported": imported})
}

// envPutMu 序列化进程内对同一 .env 的读改写。
var envPutMu sync.Mutex

// handleEnvPut 写单个环境变量：PUT /api/identities/{id}/env {key,value}。
// 保留文件其余行与注释；新键追加到文件尾；临时文件 + 原子改名。
func (s *Server) handleEnvPut(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {key, value} JSON")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" || strings.ContainsAny(req.Key, "=\n\r") {
		writeError(w, 400, "非法环境变量名")
		return
	}
	envPath := filepath.Join(id.Dir, ".env")
	envPutMu.Lock()
	defer envPutMu.Unlock()
	data, _ := os.ReadFile(envPath)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	found := false
	for i, ln := range lines {
		if k, _, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == req.Key {
			lines[i] = req.Key + "=" + req.Value
			found = true
		}
	}
	if !found {
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, req.Key+"="+req.Value)
	}
	tmp := envPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := os.Rename(tmp, envPath); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	secret := false
	lk := strings.ToLower(req.Key)
	for _, hint := range []string{"key", "secret", "token", "password"} {
		if strings.Contains(lk, hint) {
			secret = true
			break
		}
	}
	value := req.Value
	if secret && len(value) > 8 {
		value = value[:4] + "…·" + itoa(len(value)) + " chars"
	}
	writeJSON(w, 200, map[string]any{"key": req.Key, "value": value, "secret": secret})
}

// handleEnvDelete 删环境变量：DELETE /api/identities/{id}/env/{key}。
func (s *Server) handleEnvDelete(w http.ResponseWriter, r *http.Request, id *identity.Identity, key string) {
	if key == "" || strings.ContainsAny(key, "=\n\r") {
		writeError(w, 400, "非法环境变量名")
		return
	}
	envPath := filepath.Join(id.Dir, ".env")
	envPutMu.Lock()
	defer envPutMu.Unlock()
	data, err := os.ReadFile(envPath)
	if err != nil {
		writeError(w, 404, "没有可删除的环境变量")
		return
	}
	var kept []string
	found := false
	for _, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if k, _, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == key {
			found = true
			continue
		}
		kept = append(kept, ln)
	}
	if !found {
		writeError(w, 404, "变量不存在: "+key)
		return
	}
	if err := os.WriteFile(envPath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "key": key})
}
