package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"mindloop/internal/skills"
	"mindloop/internal/traj"
)

// skillsStore 解析技能库的两层目录：--identity 给定时身份级优先，
// 全局层（MINDLOOP_HOME/skills）永远在场。返回 Store 与"写入层"
// （install/init 的落点）。
func (c *CLI) skillsStore(cmd *cobra.Command) (skills.Store, string, error) {
	id, err := c.extensionIdentity(cmd)
	if err != nil {
		return skills.Store{}, "", err
	}
	global := filepath.Join(traj.Home(), "skills")
	dirs := []string{global}
	installDir := global
	if id != nil {
		installDir = filepath.Join(id.Dir, "skills")
		dirs = []string{installDir, global}
	}
	return skills.Store{Dirs: dirs}, installDir, nil
}

// newSkillsCmd 是 Agent Skills 标准（SKILL.md）的技能库管理入口。
// 模型侧经系统提示的索引段 + cat 正文渐进披露；这里是人的管理面。
func (c *CLI) newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Agent Skills 标准技能库（SKILL.md）",
		Long: `技能是 Agent Skills 开放标准（agentskills.io）的目录形态：
一个目录 + 一份 SKILL.md（YAML frontmatter：name/description 必填；
markdown 正文）。索引进系统提示，正文由模型按需读取。

两层目录：身份级 <身份>/skills 覆盖全局 ~/.mindloop/skills 的同名
技能。`,
	}
	extensionFlags(cmd)

	cmd.AddCommand(
		c.newSkillsListCmd(),
		c.newSkillsShowCmd(),
		c.newSkillsPromptCmd(),
		c.newSkillsInstallCmd(),
		c.newSkillsInitCmd(),
		c.newSkillsRemoveCmd(),
	)
	return cmd
}

func (c *CLI) newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部技能（身份级遮蔽全局）",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			items, errs := store.List()
			for _, e := range errs {
				fmt.Fprintf(c.stderr, "⚠ %v\n", e)
			}
			if len(items) == 0 {
				fmt.Fprintln(c.stdout, "（没有技能——用 skills init 或 skills install 安装）")
				return nil
			}
			for _, it := range items {
				desc := traj.OneLine(it.Description, 70)
				fmt.Fprintf(c.stdout, "%-20s %-8s %s\n", it.Name, it.Source, desc)
			}
			return nil
		},
	}
}

func (c *CLI) newSkillsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "打印技能的完整 SKILL.md",
		Args:  exactArgs(1, "用法: mindloop skills show <name> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			skill, err := store.Get(args[0])
			if err != nil {
				return c.fail(err)
			}
			data, err := os.ReadFile(filepath.Join(skill.Dir, skills.SkillFile))
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "# %s（%s 层）\n", skill.Name, skill.Source)
			fmt.Fprintln(c.stdout, strings.TrimRight(string(data), "\n"))
			return nil
		},
	}
}

func (c *CLI) newSkillsPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt",
		Short: "打印进系统提示的技能索引段（调试用）",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintln(c.stdout, store.PromptSection())
			return nil
		},
	}
}

func (c *CLI) newSkillsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <本地目录|owner/repo>",
		Short: "安装技能（本地目录或 GitHub 仓库）",
		Long: `安装源两种形态：
  - 本地目录：含 SKILL.md 则整个目录是一个技能；否则扫描直接子目录
    （多技能仓库）。
  - owner/repo：git clone --depth 1 到临时目录后按本地目录处理。
安装前按标准校验，不合格零落盘；同名已存在时报错（先 remove）。`,
		Args: exactArgs(1, "用法: mindloop skills install <目录|owner/repo> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, installDir, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			names, err := skills.Install(c.ctx, installDir, args[0])
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已安装到 %s：%s\n", installDir, strings.Join(names, ", "))
			return nil
		},
	}
}

func (c *CLI) newSkillsInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <name>",
		Short: "脚手架一个新技能（标准 frontmatter 模板）",
		Args:  exactArgs(1, "用法: mindloop skills init <name> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, installDir, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			dir := filepath.Join(installDir, args[0])
			if err := skills.Init(dir); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已创建 %s——编辑 SKILL.md 的 description 与正文\n", dir)
			return nil
		},
	}
}

func (c *CLI) newSkillsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "删除技能（身份级与全局同名时删高优先层）",
		Args:  exactArgs(1, "用法: mindloop skills remove <name> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, installDir, err := c.skillsStore(cmd)
			if err != nil {
				return c.fail(err)
			}
			dir, err := skills.Remove(skills.Store{Dirs: []string{installDir}}, args[0])
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已删除 %s\n", dir)
			return nil
		},
	}
}
