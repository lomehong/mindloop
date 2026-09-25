package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/skills"
)

// identitySkillsDir 是身份级技能库；globalSkillsDir 是全局层
// （MINDLOOP_HOME/skills——identity.Home() 的兄弟目录）。
func identitySkillsDir(id *identity.Identity) string {
	return filepath.Join(id.Dir, "skills")
}

func globalSkillsDir() string {
	return filepath.Join(filepath.Dir(identity.Home()), "skills")
}

// skillsView 是 viewer 技能页的响应契约：合并后的技能列表 +
// 解析失败条目（坏技能可见但不隐藏——隐藏等于掩盖）。
type skillsView struct {
	Identity map[string]string `json:"identity"`
	Skills   []skillsEntry     `json:"skills"`
	Problems []string          `json:"problems"`
}

type skillsEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // identity | global
	Dir         string `json:"dir"`
}

// handleSkillsList GET /api/identities/{id}/skills。
func (s *Server) handleSkillsList(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	store := skills.Store{Dirs: []string{identitySkillsDir(id), globalSkillsDir()}}
	items, errs := store.List()
	out := skillsView{
		Identity: map[string]string{"id": id.Name, "name": id.Name},
		Skills:   []skillsEntry{},
		Problems: []string{},
	}
	for _, e := range errs {
		out.Problems = append(out.Problems, e.Error())
	}
	for _, it := range items {
		out.Skills = append(out.Skills, skillsEntry{
			Name: it.Name, Description: it.Description, Source: it.Source, Dir: it.Dir,
		})
	}
	writeJSON(w, 200, out)
}

// handleSkillsInstall POST /api/identities/{id}/skills {source}——
// 安装到身份级技能库（本地目录或 GitHub owner/repo）。
func (s *Server) handleSkillsInstall(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {source} JSON")
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeError(w, 400, "缺少安装源（本地目录或 owner/repo）")
		return
	}
	names, err := skills.Install(identitySkillsDir(id), req.Source)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "installed": names})
}

// handleSkillsDelete DELETE /api/identities/{id}/skills/{name}——
// 只允许删除身份层技能；全局层是共享资产，经 CLI 管理。
func (s *Server) handleSkillsDelete(w http.ResponseWriter, r *http.Request, id *identity.Identity, name string) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		writeError(w, 400, "非法技能名")
		return
	}
	store := skills.Store{Dirs: []string{identitySkillsDir(id), globalSkillsDir()}}
	skill, err := store.Get(name)
	if err != nil {
		writeError(w, 404, "技能不存在: "+name)
		return
	}
	if skill.Source != "identity" {
		writeError(w, 400, "全局层技能请经 CLI 管理（mindloop skills remove "+name+"）")
		return
	}
	if err := os.RemoveAll(skill.Dir); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": name})
}
