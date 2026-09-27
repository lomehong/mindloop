---
name: daily-activity-report
description: 生成当日电脑活动日报：读取 activity/raw 采样数据，归纳时间线、应用排行与观察，落盘 reports/daily 报告并汇报到对话流
---

# 活动日报（daily-activity-report）

把当天的电脑活动采样归纳成一份供人快速阅读的日报。纯本地处理，零外部依赖：
数据就在你的身份目录里。

## 数据源

`"$MINDLOOP_IDENTITY_DIR"/activity/raw/<日期>.jsonl` —— 每行一条 JSON 采样：

```json
{"ts":"2026-09-27T10:22:18+08:00","process":"Qoder IDE","title":"exec.go - mindloop - Qoder IDE","idle_sec":0,"away":false,"locked":false}
```

- `ts` 本地时间；`process` 前台进程名；`title` 窗口标题（密码管理器等敏感窗口的标题已由采样端清空）；
- `idle_sec` 空闲秒数（-1 = 读取失败）；`away=true` = 锁屏或空闲 ≥300s。

注意：任务运行时你的工作目录是运行目录（runs/...），**不是**身份目录——
所有路径一律用 `"$MINDLOOP_IDENTITY_DIR"` 前缀。文件是**逐行 JSON**（不是 JSON 数组）。

## 时长估算

采样间隔 2 分钟，**每条记录代表约 2 分钟**：某应用时长 ≈ 该应用行数 × 2 分钟。
连续相同的 process/title 视为一段；`away=true` 的行归入"离开/锁屏"桶，不计应用时长。

## 报告结构与落盘

落盘到 `"$MINDLOOP_IDENTITY_DIR"/reports/daily/<日期>.md`（当天已有则覆盖，以最新为准；
目录不存在先 `mkdir -p`）：

```markdown
# 活动日报 — 2026-09-27（周日）

## 概览
- 采样 N 行（覆盖 09:30–21:00）；离开/锁屏 M 行；估算活跃约 H 小时

## 时间线
- 09:00–10:00 主要在做 X（窗口/应用）
- 10:00–12:00 ……
- （离开时段标"离开"）

## 应用排行（估算）
1. Qoder IDE — 约 3 小时 20 分（100 行）
2. ……

## 观察
- 1–3 条数据直接支持的模式；没有把握的推测不写
```

## 如实纪律

- 当天文件不存在或 0 行：报告写明"今天没有采样数据（心智可能未运行）"，**不要编造**；
- 行数明显低于预期：在概览与观察里注明数据断档与可能的缺口时段；
- 所有时长都是估算，报告里说明估算方式（行数 × 2 分钟）。

## 汇报（必做）

报告落盘后，把 **≤500 字**摘要发到对话流（会出现在对话页）：

```bash
"$MINDLOOP_EXE" traj append 5518efef message --field from=ada --field to=operator --field source=task --content "活动日报已生成（2026-09-27）：<两三句核心结论>；完整报告：$MINDLOOP_IDENTITY_DIR/reports/daily/2026-09-27.md"
```

`5518efef` 是你的主轨迹前缀（对应 trajectories/5518efef-mind-ada）；
若报找不到轨迹，先 `"$MINDLOOP_EXE" traj list` 查看实际 id 再替换。
字段必须用 `--field K=V` 形式（裸 `--from` 不生效）。

## 收尾

FINAL 必须写在 bash 代码块**内部**，先赋值再回显（避免被判无效）：

```bash
FINAL="活动日报完成：报告已落盘并已汇报到对话流"; echo "$FINAL"
```

格式纪律：每轮回复恰好一个 bash 代码块、块外零文本（见 format-discipline 技能）。
