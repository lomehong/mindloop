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
	// Install 自 task-11 起接收 ctx（git clone 走 CommandContext +
	// 超时）——请求 ctx 在 handler 返回时被取消，安装是同步完成
	// 的，语义正好。
	names, err := skills.Install(r.Context(), identitySkillsDir(id), req.Source)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "installed": names})
}

// skillsContent 是 GET /skills/{name} 的响应契约：SKILL.md 全文 +
// 元数据。editable=false（全局层）时前端只读展示——全局层是共享
// 资产，经 CLI 管理。
type skillsContent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Dir         string `json:"dir"`
	Content     string `json:"content"`
	Editable    bool   `json:"editable"`
}

// lookupSkill 按名取技能并校验名字合法性（防路径穿越）。三个
// handler 共用的守门：非法名 400、不存在 404。
func lookupSkill(w http.ResponseWriter, id *identity.Identity, name string) (skills.Skill, bool) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		writeError(w, 400, "非法技能名")
		return skills.Skill{}, false
	}
	store := skills.Store{Dirs: []string{identitySkillsDir(id), globalSkillsDir()}}
	skill, err := store.Get(name)
	if err != nil {
		writeError(w, 404, "技能不存在: "+name)
		return skills.Skill{}, false
	}
	return skill, true
}

// handleSkillsGet GET /api/identities/{id}/skills/{name}——返回
// SKILL.md 全文（编辑器的先读后写；不带编辑语义，全局层也能读）。
func (s *Server) handleSkillsGet(w http.ResponseWriter, _ *http.Request, id *identity.Identity, name string) {
	skill, ok := lookupSkill(w, id, name)
	if !ok {
		return
	}
	data, err := os.ReadFile(filepath.Join(skill.Dir, skills.SkillFile))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, skillsContent{
		Name: skill.Name, Description: skill.Description, Source: skill.Source,
		Dir: skill.Dir, Content: string(data), Editable: skill.Source == "identity",
	})
}

// handleSkillsUpdate PUT /api/identities/{id}/skills/{name} {content}——
// 全文替换 SKILL.md。顺序钉死为校验→写盘：校验失败的半成品绝不落
// （技能层是按目录整读的，一个坏 SKILL.md 只会被列表跳过并落到
// problems，编辑就无从谈起了）。只允许身份层；全局层经 CLI 管理
// （与 DELETE 同一红线）。写入统一 LF 行尾。
func (s *Server) handleSkillsUpdate(w http.ResponseWriter, r *http.Request, id *identity.Identity, name string) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {content} JSON")
		return
	}
	skill, ok := lookupSkill(w, id, name)
	if !ok {
		return
	}
	if skill.Source != "identity" {
		writeError(w, 400, "全局层技能请经 CLI 管理（mindloop skills remove "+name+" 同理）")
		return
	}
	content := strings.ReplaceAll(req.Content, "\r\n", "\n")
	if err := skills.Validate(skill.Dir, content); err != nil {
		writeError(w, 400, "SKILL.md 校验失败，未保存: "+err.Error())
		return
	}
	path := filepath.Join(skill.Dir, skills.SkillFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		writeError(w, 500, "写入失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "saved": name})
}

// handleSkillsDelete DELETE /api/identities/{id}/skills/{name}——
// 只允许删除身份层技能；全局层是共享资产，经 CLI 管理。
func (s *Server) handleSkillsDelete(w http.ResponseWriter, _ *http.Request, id *identity.Identity, name string) {
	skill, ok := lookupSkill(w, id, name)
	if !ok {
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
