package cli

import (
	"path/filepath"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
	"mindloop/internal/traj"
)

// identityExtension 装配身份的扩展能力面（Agent Skills 技能库 +
// MCP 服务器），供 mind run / chat 一次性取齐：
//   - SkillsDirs：技能库两层目录（身份级在前遮蔽全局）
//   - MCPServers：合并后的 MCP 服务器名清单（只进系统提示的名字，
//     工具由模型经 CLI 按需探索）
//   - ExtraEnv：注入沙箱的环境变量——agent 在 bash 里用同一套
//     CLI 探索这些能力（$MINDLOOP_EXE mcp ... / cat $SKILLS_DIR/...）
func identityExtension(id *identity.Identity) (skillsDirs, mcpNames, extraEnv []string, err error) {
	skillsDirs = []string{
		filepath.Join(id.Dir, "skills"),
		filepath.Join(traj.Home(), "skills"),
	}
	cfg, err := mcp.LoadConfig(
		filepath.Join(traj.Home(), "mcp.json"),
		filepath.Join(id.Dir, "mcp.json"),
	)
	if err != nil {
		// MCP 配置坏了不能拦住心智启动——降级为无 MCP 并提示。
		// 调用方负责把 err 当警告展示。
		return skillsDirs, nil, []string{
			"MINDLOOP_IDENTITY_DIR=" + id.Dir,
			"SKILLS_DIR=" + skillsDirs[0],
		}, err
	}
	return skillsDirs, cfg.Names(), []string{
		"MINDLOOP_IDENTITY_DIR=" + id.Dir,
		"SKILLS_DIR=" + skillsDirs[0],
	}, nil
}
