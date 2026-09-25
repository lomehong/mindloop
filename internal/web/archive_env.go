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
	"regexp"
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
			rel = filepath.ToSlash(rel)
			// 与单身份导出同一条铁律（archiveExcluded 共用）：.env
			// 与 run/ 控制面绝不进可携带归档。漏掉这条会把每个身份
			// 的 API key 打进"导出全部"的下载产物。top=1：第 0 层
			// 是身份名。
			if archiveExcluded(rel, 1) {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			// 条目名统一 identities/<name>/... 形态——与
			// writeIdentityArchive 一致，也是 handleImport 唯一
			// 接受的形态（外来路径一律拒绝落盘）。
			hdr.Name = "identities/" + rel
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
		// 归档只认 identities/ 子树（handleExport 的产物形态）。
		// 外来路径（如 x/y、etc/passwd）绝不落盘——否则任何两段
		// 路径都会在身份根下创建任意目录，可覆盖身份文件构成
		// 持久化注入链。
		if !strings.HasPrefix(name, "identities/") {
			writeError(w, 400, "归档条目不在 identities/ 下，拒绝导入: "+name)
			return
		}
		trimmed := strings.TrimPrefix(name, "identities/")
		parts := strings.Split(strings.TrimSuffix(trimmed, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		if !isSafeIdentName(parts[0]) || isBadIdentSegment(parts[0]) {
			writeError(w, 400, "归档包含非法身份名: "+parts[0])
			return
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

// envKeyRe 是环境变量名白名单：dotenv 生态的标准形态。带空格/引号/
// #/= 的键名会与 .env 的行解析产生歧义（注释边界、同名异写、变量
// 注入），PUT 与 DELETE 共用同一条规则。
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// isEnvKey 报告 key 是否为合法环境变量名。
func isEnvKey(key string) bool { return envKeyRe.MatchString(key) }

// isSecretEnvKey 按键名判定是否敏感（含 key/secret/token/password
// 子串，大小写不敏感）——env 读写两个端点共用同一判定。
func isSecretEnvKey(key string) bool {
	lk := strings.ToLower(key)
	for _, hint := range []string{"key", "secret", "token", "password"} {
		if strings.Contains(lk, hint) {
			return true
		}
	}
	return false
}

// maskSecretValue 对敏感值做无门槛脱敏：命中即不回显任何内容字节，
// 只给长度提示（与 parseEnvRedacted 同一形态）。此前 PUT 响应对
// ≤8 字符的值原样回显——短密钥同样不该泄漏。
func maskSecretValue(v string) string {
	return "[REDACTED · " + itoa(len(v)) + " chars]"
}

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
	if !isEnvKey(req.Key) {
		writeError(w, 400, "非法环境变量名（仅允许字母/数字/下划线，且不以数字开头）")
		return
	}
	// 值也不允许换行：dotenv 是按行解析的，换行能注入任意
	// 附加变量行（如再塞一个 MINDLOOP_BASE_URL）。
	if strings.ContainsAny(req.Value, "\n\r") {
		writeError(w, 400, "环境变量值不能包含换行")
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
	secret := isSecretEnvKey(req.Key)
	value := req.Value
	if secret {
		value = maskSecretValue(value)
	}
	writeJSON(w, 200, map[string]any{"key": req.Key, "value": value, "secret": secret})
}

// handleEnvDelete 删环境变量：DELETE /api/identities/{id}/env/{key}。
func (s *Server) handleEnvDelete(w http.ResponseWriter, r *http.Request, id *identity.Identity, key string) {
	if !isEnvKey(key) {
		writeError(w, 400, "非法环境变量名（仅允许字母/数字/下划线，且不以数字开头）")
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
