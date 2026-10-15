// 体征层（avatar.md §3.4）：呼吸/心跳/食量/睡眠/痛觉——一条跨视图
// 常驻的生命体征带。任何时候瞥一眼，都知道它"活着没有、饿不饿、
// 疼不疼"。隐喻机制账（avatar.md §3.2）：饿 = 水位 ≥80%（真的水位）、
// 疼 = 熔断冷却（真的熔断）、睡 = 安睡/打盹（真的无活动）——界面上
// 不出现任何机制不支撑的拟人词。
//
// 三化身同源（avatar.md §3.3）：体征条与桌宠（脸）、舞台核心球（身）
// 消费同一份五态信号系（pet-state.PetSignals → deriveMood）——同一
// 只生命在三个距离上的读数一致。桌面壳的 SSE 专有信号（正在说话/
// 刚有警情）体征条拿不到，缺哪个信号就不显示哪个派生态，不猜。
//
// 纯函数：信号全是相对量（距今秒数/毫秒数），无绝对时钟依赖。

import { fmtDuration } from "~/components/activity-badge";
import {
  deriveMood,
  MOOD_LABEL,
  type PetMood,
  type PetSignals,
} from "~/lib/pet-state";

export interface Vitals {
  /** 息（呼吸）：五态派生 + 标签。 */
  mood: PetMood;
  moodLabel: string;
  /** 心跳：最近一步距今的短时长（"3m"）；null = 无读数。 */
  heartbeat: string | null;
  /** 食量：水位百分比；null = 未设预算（此时不显示饿）。 */
  appetitePct: number | null;
  /** 饿是真的饿：水位 ≥80%（与 deriveMood 的 hungry 同一判据）。 */
  hungry: boolean;
  /** 睡眠：awake 清醒 / doze 打盹（30 分钟无步骤）/ asleep 安睡
   * （activity 的 asleep 态——真的无唤醒窗口）。 */
  sleep: "awake" | "doze" | "asleep";
  /** 痛觉：1 = 熔断冷却中（真的疼），0 = 不疼。 */
  pain: 0 | 1;
}

export function vitalsFor(s: PetSignals): Vitals {
  const mood = deriveMood(s);
  const sleep: Vitals["sleep"] =
    s.activity === "asleep"
      ? "asleep"
      : s.lastStepAgeS !== null && s.lastStepAgeS > 1800
        ? "doze"
        : "awake";
  return {
    mood,
    moodLabel: MOOD_LABEL[mood],
    heartbeat: fmtDuration(s.lastStepAgeS),
    appetitePct:
      s.budgetRatio === null ? null : Math.round(s.budgetRatio * 100),
    hungry: s.budgetRatio !== null && s.budgetRatio >= 0.8,
    sleep,
    pain: s.cooling ? 1 : 0,
  };
}

export const SLEEP_LABEL: Record<Vitals["sleep"], string> = {
  awake: "清醒",
  doze: "打盹",
  asleep: "安睡",
};
