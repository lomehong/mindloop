// 注意力面（avatar.md §3.1 感知 → 注意力；样张 §4）的聚合逻辑层：
// 它正在看什么、听见了什么、什么被安静略过——视觉分级复用神经系统：
// S2 一张卡、S1 一行、S0 折叠计数，界面不绕过它的降噪直接铺日志。
// 数据 = 轨迹 event 步骤（internal/mind/sensorrun.go writeEvent：
// salience s0-s2；s3 走 alert 有自己的通道）。行数窗口，展示必须
// 标注「最近 N 步内」。
//
// 学习期预演：learning-cap 期间被封在 s1 的消息（raw.reason 带
// learning-cap），转正后会以 s2 叫醒——按感官计数给预演读数，转正
// 由用户点头（avatar.md §4.3 放行）。

import type { NormalizedStep } from "~/lib/types";

export interface AttentionItem {
  stepId: string;
  ts: string;
  source: string;
  digest: string;
  reason: string;
}

export interface AttentionStream {
  s2: AttentionItem[];
  s1: AttentionItem[];
  /** s0 背景沉淀：按感官计数（不叫醒，喂记忆代谢）。 */
  s0BySource: { source: string; count: number }[];
  s0Total: number;
  /** 学习期预演：被 learning-cap 压制的消息按感官计数。 */
  learningCapBySource: { source: string; count: number }[];
  /** 统计窗口首步 ts；调用方如实标注「最近 N 步内」。 */
  windowStart: string | null;
}

function itemOf(s: NormalizedStep): AttentionItem {
  return {
    stepId: s.step_id,
    ts: s.ts,
    source: typeof s.raw.source === "string" ? s.raw.source : "?",
    digest: typeof s.raw.digest === "string" ? s.raw.digest : s.preview,
    reason: typeof s.raw.reason === "string" ? s.raw.reason : "",
  };
}

export function attentionStream(steps: NormalizedStep[]): AttentionStream {
  const s2: AttentionItem[] = [];
  const s1: AttentionItem[] = [];
  const s0 = new Map<string, number>();
  const learning = new Map<string, number>();
  let windowStart: string | null = null;

  for (const s of steps) {
    if (windowStart === null) windowStart = s.ts;
    if (s.type === "alert") {
      // s3 告警与 s2 同样"值得现在看"（它已经走了告警通道）。
      if (s.raw.salience === "s3" || s.type === "alert") s2.push(itemOf(s));
      continue;
    }
    if (s.type !== "event") continue;
    const sal = s.raw.salience;
    if (sal === "s2") {
      s2.push(itemOf(s));
    } else if (sal === "s1") {
      const it = itemOf(s);
      s1.push(it);
      if (typeof s.raw.reason === "string" && s.raw.reason.includes("learning-cap")) {
        learning.set(it.source, (learning.get(it.source) ?? 0) + 1);
      }
    } else if (sal === "s0") {
      const it = itemOf(s);
      s0.set(it.source, (s0.get(it.source) ?? 0) + 1);
    }
  }

  const byCountDesc = (m: Map<string, number>) =>
    [...m.entries()]
      .map(([source, count]) => ({ source, count }))
      .sort((a, b) => b.count - a.count);

  return {
    // 轨迹是追加序（旧→新），展示新→旧。
    s2: s2.reverse(),
    s1: s1.reverse(),
    s0BySource: byCountDesc(s0),
    s0Total: [...s0.values()].reduce((a, b) => a + b, 0),
    learningCapBySource: byCountDesc(learning),
    windowStart,
  };
}


/** 近窗口被 learning-cap 压制的消息总数——"感官在学习期"的轨迹实证
 * （配置判据不可靠：LearningDays 带 omitempty，靠缺省学习期的新感官
 * 不吐字段；且"配置了天数"≠"仍在期内"）。null 安全：无窗口即 0。 */
export function learningCapTotal(steps: NormalizedStep[]): number {
  let n = 0;
  for (const s of steps) {
    if (
      s.type === "event" &&
      typeof s.raw.reason === "string" &&
      s.raw.reason.includes("learning-cap")
    ) {
      n += 1;
    }
  }
  return n;
}
