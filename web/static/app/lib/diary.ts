// 日记（avatar.md §5 时间望远镜）的纯逻辑层：缩放档位、步骤粒子、
// 章节卡片、14 天年轮。审计精度不减——粒子/生命线的点击都跳泳道
// ?step=（TimelineView 全量能力是显微镜档，本页只是拉远）。
//
// 窗口诚实：轨迹查询是行数窗口（mindlog tail），1h 粒子与 24h 生命
// 线的覆盖起点由调用方如实标注；14 天年轮来自 usage 台账（天粒度，
// 无窗口问题）。

import { timelineColor } from "~/lib/step-colors";
import type { NormalizedStep, Recap, UsageDay } from "~/lib/types";

export type DiaryZoom = "now" | "1h" | "24h" | "14d";

export const DIARY_ZOOMS: {
  key: DiaryZoom;
  label: string;
  hint: string;
}[] = [
  { key: "now", label: "此刻", hint: "舞台 · 它现在怎么样" },
  { key: "1h", label: "1 小时", hint: "步骤粒子 · 显微镜前的最近一段" },
  { key: "24h", label: "24 小时", hint: "生命线 · 一天的活动痕迹" },
  { key: "14d", label: "14 天", hint: "年轮与章节 · 与周报同源" },
];

export const ZOOM_MS: Record<Exclude<DiaryZoom, "now" | "14d">, number> = {
  "1h": 3_600_000,
  "24h": 24 * 3_600_000,
};

/** 相邻档位切换的节流（滚轮=时间旅行，350ms 原型约定）。 */
export const WHEEL_THROTTLE_MS = 350;

export function stepZoom(i: number, total = DIARY_ZOOMS.length): DiaryZoom {
  const clamped = Math.max(0, Math.min(total - 1, i));
  return DIARY_ZOOMS[clamped].key;
}

export interface DiaryParticle {
  stepId: string;
  ts: string;
  /** 五色分类学实心点类（step-colors）。 */
  dot: string;
  /** 悬停读数：类型 + 单行摘要。 */
  title: string;
}

/** 窗口内的步骤粒子（新→旧）。cap 之外的如实折叠为计数——渲染上限
 * 不改变事件集。 */
export function particlesInWindow(
  steps: NormalizedStep[],
  now: number,
  windowMs: number,
  cap = 400
): { particles: DiaryParticle[]; overflow: number } {
  const cutoff = now - windowMs;
  const inWindow: NormalizedStep[] = [];
  for (const s of steps) {
    const ms = Date.parse(s.ts);
    if (!Number.isNaN(ms) && ms >= cutoff) inWindow.push(s);
  }
  // steps 来自 tail 窗口（新→旧？后端 Steps() 顺序即追加序，旧→新），
  // 排序统一新→旧。
  inWindow.sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts));
  const particles = inWindow.slice(0, cap).map((s) => ({
    stepId: s.step_id,
    ts: s.ts,
    dot: timelineColor(s.type),
    title: `${s.type} · ${s.preview.slice(0, 80)}`,
  }));
  return { particles, overflow: Math.max(0, inWindow.length - cap) };
}

/** 窗口覆盖起点（首步 ts）；无步或首步晚于窗口起点 = 覆盖不足。 */
export function coverageStartTs(steps: NormalizedStep[]): number | null {
  if (steps.length === 0) return null;
  const ms = Date.parse(steps[0].ts);
  return Number.isNaN(ms) ? null : ms;
}

export function coveredSince(steps: NormalizedStep[], now: number, windowMs: number): boolean {
  const start = coverageStartTs(steps);
  return start !== null && start <= now - windowMs;
}

// ---- 14 天年轮（usage 台账天粒度）------------------------------------------

export interface RingDay {
  day: string;
  calls: number;
}

/** 近 14 天年轮：每天一柱，有柱 = 那天有调用，空段 = 休息。
 * 台账只记调用与 token，以 calls 为活动信号（home 的 ringCalls 迁入
 * 共用）。 */
export function ring14(daily: [string, UsageDay][] | undefined, now: Date = new Date()): RingDay[] {
  const byDay = new Map(daily ?? []);
  const out: RingDay[] = [];
  for (let i = 13; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 86_400_000);
    const key = d.toISOString().slice(0, 10);
    out.push({ day: key, calls: byDay.get(key)?.calls ?? 0 });
  }
  return out;
}

// ---- 章节（recap 分集，与周报管道同源）-------------------------------------

export interface DiaryChapter {
  idx: number;
  title: string;
  summary: string;
  firstTs: string;
  lastTs: string;
  nSteps: number;
}

/** 14 天内的章节（recap 的 episodes），新→旧。 */
export function chaptersOf(recap: Recap | undefined, now: number = Date.now()): DiaryChapter[] {
  const cutoff = now - 14 * 86_400_000;
  return (recap?.episodes ?? [])
    .filter((e) => {
      const ms = Date.parse(e.last_ts);
      return !Number.isNaN(ms) && ms >= cutoff;
    })
    .sort((a, b) => Date.parse(b.last_ts) - Date.parse(a.last_ts))
    .map((e) => ({
      idx: e.idx,
      title: e.title,
      summary: e.summary,
      firstTs: e.first_ts,
      lastTs: e.last_ts,
      nSteps: e.n_steps,
    }));
}
