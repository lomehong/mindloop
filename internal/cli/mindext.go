package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
	"mindloop/internal/traj"
)

func extensionFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("identity", "", "身份名（省略时使用 MINDLOOP_IDENTITY_DIR，否则全局）")
	cmd.PersistentFlags().Bool("global", false, "只操作全局层，忽略环境中的身份")
	cmd.MarkFlagsMutuallyExclusive("identity", "global")
}

// extensionIdentity 在执行阶段读取解析后的标志，避免构造子命令时复制默认值。
func (c *CLI) extensionIdentity(cmd *cobra.Command) (*identity.Identity, error) {
	global, err := cmd.Flags().GetBool("global")
	if err != nil {
		return nil, err
	}
	if global {
		return nil, nil
	}
	if cmd.Flags().Changed("identity") {
		name, err := cmd.Flags().GetString("identity")
		if err != nil {
			return nil, err
		}
		return c.loadIdentity(name)
	}
	dir := os.Getenv("MINDLOOP_IDENTITY_DIR")
	if dir == "" {
		return nil, nil
	}
	id, err := c.loadIdentity(filepath.Base(filepath.Clean(dir)))
	if err != nil {
		return nil, fmt.Errorf("MINDLOOP_IDENTITY_DIR: %w", err)
	}
	actual, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("MINDLOOP_IDENTITY_DIR: %w", err)
	}
	expected, err := os.Stat(id.Dir)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(actual, expected) {
		return nil, fmt.Errorf("MINDLOOP_IDENTITY_DIR %q 不属于当前身份目录 %s", dir, identity.Home())
	}
	return id, nil
}

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
