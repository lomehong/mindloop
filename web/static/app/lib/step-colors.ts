// 分类学单表（docs/designs/ui-language.md §2.3）：步骤类型按语义归五色，
// 不再按类型散配生色。颜色全部引用 app.css 的 --moss/--lake/--plum/
// --resin/--clay 变量（chip-* 类带混色对比度修正），双主题自动随动。
//
// 苔 moss=思考 · 湖 lake=观察与外来 · 梅 plum=心智之言与结构事件 ·
// 树脂 resin=行动与警示 · 陶土 clay=危险与错误 · 灰 faint=机制噪声。

import type { StepType } from "~/lib/types";

export type Taxonomy = "moss" | "lake" | "plum" | "resin" | "clay" | "faint";

export interface StepColor {
  /** card 左规 accent 类 */
  border: string;
  /** 类型标签芯片类 */
  chip: string;
  /** 实心圆点/方块类（泳道头、图例、年轮条） */
  dot: string;
  taxonomy: Taxonomy;
}

const T: Record<Taxonomy, Omit<StepColor, "taxonomy">> = {
  moss: { border: "border-l-moss", chip: "chip-moss", dot: "bg-moss" },
  lake: { border: "border-l-lake", chip: "chip-lake", dot: "bg-lake" },
  plum: { border: "border-l-plum", chip: "chip-plum", dot: "bg-plum" },
  resin: { border: "border-l-resin", chip: "chip-resin", dot: "bg-resin" },
  clay: { border: "border-l-clay", chip: "chip-clay", dot: "bg-clay" },
  faint: { border: "border-l-faint", chip: "chip-faint", dot: "bg-faint" },
};

const t = (taxonomy: Taxonomy): StepColor => ({ ...T[taxonomy], taxonomy });

const COLORS: Record<string, StepColor> = {
  // 苔：思考
  thought: t("moss"),
  "tp-thought": t("moss"),
  reasoning: t("moss"),
  // 湖：观察与外来（操作员发言、反馈）
  observation: t("lake"),
  "human-msg": t("lake"),
  feedback: t("lake"),
  // 梅：心智之言与结构事件
  message: t("plum"),
  "agent-msg": t("plum"),
  final: t("plum"),
  fork: t("plum"),
  merge: t("plum"),
  trajectory: t("plum"),
  "run-summary": t("plum"),
  // 树脂：行动（run 调度、shell 命令与输出）
  action: t("resin"),
  "shellm-run": t("resin"),
  "shell-output": t("resin"),
  // 陶土：危险与错误（现数据暂无此族步骤类型，预置映射）
  error: t("clay"),
  alert: t("clay"),
  // 灰：机制噪声
  prompt: t("faint"),
  idle: t("faint"),
};

const FALLBACK = t("faint");

export function stepColor(type: StepType | string): StepColor {
  return COLORS[type] ?? FALLBACK;
}

/** 实心色块（泳道方格、年轮条、时间轴分段）。 */
export function timelineColor(type: StepType | string): string {
  return stepColor(type).dot;
}

/** 该步骤是否属于"错误/告警"族（生命线陶土刻度；含非零退出的 shell）。 */
export function isErrorStep(step: {
  type: string;
  raw?: Record<string, unknown>;
}): boolean {
  if (stepColor(step.type).taxonomy === "clay") return true;
  if (step.type === "shell-output") {
    const exit = step.raw?.exit;
    if (typeof exit === "number" && exit !== 0) return true;
    if (step.raw?.timed_out === true) return true;
  }
  return false;
}
