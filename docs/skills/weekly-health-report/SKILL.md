---
name: weekly-health-report
description: 生成一周模型健康与用量信号周报：汇总 llm-health 快照、一周调用趋势与模型分布，落盘 reports/health 并汇报
---

# 健康周报（weekly-health-report）

汇总最近 7 天（**含今天**，本地时区；不要按自然周回退——"上周"一律按
这个窗口理解）的模型调用健康信号为一份简短周报：心跳状态、调用趋势、
模型分布。纯本地处理。周一早上由日程触发（与活动周报错峰）。

## 数据源（都在 $MINDLOOP_IDENTITY_DIR 下）

- `llm-health.json` —— 当前健康快照：
  `{"last_ok":"…","last_error_at":"…","last_check":"…","consecutive_errors":0}`
  （**只有当前值，没有逐日历史**——历史信号从用量台账的调用密度间接推断，
  报告里如实说明这一口径）；
- `usage/llm-usage.jsonl` —— 每行一次模型调用（ts/model/prompt_tokens/
  completion_tokens/provider），按天聚合出调用趋势。

任务运行时你的工作目录是运行目录，**所有路径一律加 `"$MINDLOOP_IDENTITY_DIR"` 前缀**。

## 执行策略（重要——轮次预算只有 8 轮）

**第一轮就写一个完整的 python 脚本**（写入临时文件并执行），一次完成：
读 llm-health.json 与 usage 台账 → 聚合 → 生成报告 markdown 并落盘 →
打印摘要文本。不要分多轮试探；第二轮用打印的摘要做 `traj append` 汇报，
第三轮 FINAL。台账是逐行 JSON，脚本里 `json.loads` 逐行解析。

## 报告结构与落盘

落盘到 `"$MINDLOOP_IDENTITY_DIR"/reports/health/<本周一日期>.md`（已存在则覆盖；
目录不存在先 `mkdir -p`）：

```markdown
# 健康周报 — 2026-09-21 ~ 09-27

## 心跳状态（当前快照）
- 最近成功：09-27 14:39；最近错误：09-27 10:42；连续失败 0 次

## 一周调用趋势
- 合计 312 次调用；日均 45 次；峰值周三（86 次）、谷值周日（4 次）
- 模型分布：glm-5.3-flash 310 次、echo 2 次
- token 合计：prompt 182K / completion 45K

## 观察
- 1–2 条数据支持的判断（如：调用集中在工作时段、无连续失败迹象）
```

## 如实纪律

- 心跳是**当前快照**，不等于一周每天都有此状态——报告里写明口径；
- 台账不存在或 0 行：写"本周无调用记录"，**不要编造**；
- `consecutive_errors > 0` 或 `last_error_at` 在 48h 内：提到摘要第一条并建议关注。

## 汇报（必做）

报告落盘后，把 **≤300 字**摘要发到任务文本指定的地址（未指明用 `operator`）：

```bash
"$MINDLOOP_EXE" traj append 5518efef message --field from=ada --field to=<汇报目标地址> --field source=report --content "健康周报（09-21~09-27）：<心跳状态 + 一句趋势>；完整报告：$MINDLOOP_IDENTITY_DIR/reports/health/2026-09-21.md"
```

`5518efef` 是你的主轨迹前缀。字段必须用 `--field K=V` 形式。

**路由纪律（必须遵守）**：`to` 用**任务文本里指定的地址**（如 `wecom:HongYan`），
绝不用 `operator` 顶替——operator 只是本地对话流，人看不到推送；任务文本与
模板不一致时以任务文本为准。发完检查轨迹：若 to 不是指定地址，立即用正确
地址补发一条。

## 收尾

FINAL 必须写在 bash 代码块**内部**，先赋值再回显：

```bash
FINAL="健康周报完成：报告已落盘并已汇报"; echo "$FINAL"
```

格式纪律：每轮回复恰好一个 bash 代码块、块外零文本（见 format-discipline 技能）。
