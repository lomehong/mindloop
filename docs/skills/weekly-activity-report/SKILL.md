---
name: weekly-activity-report
description: 生成最近 7 天的电脑活动与模型用量周报：逐日聚合 activity 采样、合计 usage 台账，落盘 reports/weekly 周报并汇报到默认外发地址
---

# 活动周报（weekly-activity-report）

把最近 7 天（**含今天**，本地时区；不要按自然周回退到上周一之前）的电脑
活动采样与模型用量台账聚合为一份周度报告。
纯本地处理，零外部依赖：数据都在你的身份目录里。周一早上由日程触发；
平时手动触发生成的是"截至今天的最近 7 天"。

## 数据源（都在 $MINDLOOP_IDENTITY_DIR 下）

- `activity/raw/<日期>.jsonl` × 7 天 —— 每行一条采样
  `{"ts":"…","process":"…","title":"…","idle_sec":0,"away":false,"locked":false}`；
  采样间隔 2 分钟，每行 ≈ 2 分钟；`away=true` 归"离开"桶。
- `usage/llm-usage.jsonl` —— 每行一次模型调用
  `{"ts":"…","prompt_tokens":N,"completion_tokens":N,"model":"…","provider":"…"}`。

任务运行时你的工作目录是运行目录，**所有路径一律加 `"$MINDLOOP_IDENTITY_DIR"` 前缀**；
文件都是**逐行 JSON**（不是数组）。日期清单用近 7 天（含今天），本地时区。

## 执行策略（重要——轮次预算只有 8 轮）

**第一轮就写一个完整的 python 脚本**（写入临时文件并执行），一次完成：
读全部数据文件 → 聚合 → 生成报告 markdown 并落盘 → 打印摘要文本。
不要分多轮试探数据、不要逐文件手工统计——每轮只有一次 bash 机会，
脚本要一次写对。第二轮用脚本打印的摘要做 `traj append` 汇报，第三轮 FINAL。
数据文件是逐行 JSON，脚本里用 `json.loads` 逐行解析；文件不存在按断档处理。

## 聚合方法

- **每日活跃估算** = 当天非 away 行数 × 2 分钟；连续相同 process 合并成段；
- **应用排行周合计** = 7 天内按 process 累计行数 × 2 分钟，取 Top 5；
- **模型用量周合计** = 按 model 分组累计 prompt_tokens / completion_tokens 与调用次数，
  并给出按天的调用次数分布（哪天最多/最少）；
- awk 或 python 皆可（`python` 无第三方库要求，逐行 json.loads）。

## 报告结构与落盘

落盘到 `"$MINDLOOP_IDENTITY_DIR"/reports/weekly/<本周一日期>.md`（已存在则覆盖；
目录不存在先 `mkdir -p`）：

```markdown
# 活动周报 — 2026-09-21 ~ 09-27

## 概览
- 覆盖 7 天；有采样数据 5 天；周活跃合计约 23 小时（估算）
- 数据断档：9/23 无采样（心智未运行）

## 每日一行
- 09/21 周一：约 4.2 小时，主要是 mindloop 开发
- 09/22 周二：离开为主（0.5 小时）
- ……

## 应用排行（周合计，估算）
1. Qoder IDE — 约 11 小时
2. …

## 模型用量（周合计）
- glm-5.3-flash：调用 312 次，prompt 182K tokens，completion 45K tokens
- 峰值日：周三（86 次）；最省日：周日（4 次）

## 观察
- 2–3 条数据直接支持的模式；没有把握的推测不写
```

## 如实纪律

- 缺哪天数据就写明哪天断档，**不要编造**；
- 所有时长是估算（行数 × 2 分钟），用量是台账精确合计——两者口径分开陈述；
- usage 文件不存在时写"无用量台账"，活动与用量两块互不牵连。

## 汇报（必做）

报告落盘后，把 **≤500 字**摘要发到默认外发地址（注入的任务文本里已写明
目标地址；通常是 `wecom:<userid>`，摘要会经 bridge 推送到手机）：

```bash
"$MINDLOOP_EXE" traj append 5518efef message --field from=ada --field to=<汇报目标地址> --field source=report --content "活动周报（09-21~09-27）：<核心结论 3 句以内>；完整报告：$MINDLOOP_IDENTITY_DIR/reports/weekly/2026-09-21.md"
```

`5518efef` 是你的主轨迹前缀。字段必须用 `--field K=V` 形式。

**路由纪律（必须遵守）**：`to` 用**任务文本里指定的地址**（如 `wecom:HongYan`），
绝不用 `operator` 顶替——operator 只是本地对话流，人看不到推送；任务文本与
模板不一致时以任务文本为准。发完检查轨迹：若 to 不是指定地址，立即用正确
地址补发一条。

## 收尾

FINAL 必须写在 bash 代码块**内部**，先赋值再回显：

```bash
FINAL="活动周报完成：报告已落盘并已汇报"; echo "$FINAL"
```

格式纪律：每轮回复恰好一个 bash 代码块、块外零文本（见 format-discipline 技能）。
