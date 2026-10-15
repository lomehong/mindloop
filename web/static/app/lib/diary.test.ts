import { describe, expect, it } from "vitest";

import {
  chaptersOf,
  coveredSince,
  particlesInWindow,
  ring14,
  stepZoom,
  WHEEL_THROTTLE_MS,
  type DiaryZoom,
} from "./diary";
import type { NormalizedStep, Recap } from "./types";

// now 钉在本地正午：ts 全部相对本地时区构造。
const NOW = (() => {
  const d = new Date();
  d.setHours(12, 0, 0, 0);
  return d.getTime();
})();

const at = (offsetMs: number): string => new Date(NOW - offsetMs).toISOString();

function step(partial: Partial<NormalizedStep> & { step_id: string }): NormalizedStep {
  return {
    ts: at(60_000),
    type: "action",
    source: null,
    preview: "",
    raw: {},
    run_id: null,
    ...partial,
  };
}

describe("stepZoom", () => {
  it("档位夹取", () => {
    expect(stepZoom(0)).toBe<DiaryZoom>("now");
    expect(stepZoom(2)).toBe<DiaryZoom>("24h");
    expect(stepZoom(99)).toBe<DiaryZoom>("14d");
    expect(stepZoom(-3)).toBe<DiaryZoom>("now");
  });
  it("滚轮节流是原型的 350ms", () => {
    expect(WHEEL_THROTTLE_MS).toBe(350);
  });
});

describe("particlesInWindow", () => {
  it("只收窗口内的步骤，新→旧，带五色分类学", () => {
    const { particles } = particlesInWindow(
      [
        step({ step_id: "in1", ts: at(30 * 60_000), type: "final" }),
        step({ step_id: "out", ts: at(2 * 3_600_000) }),
        step({ step_id: "in2", ts: at(5 * 60_000), type: "error" }),
      ],
      NOW,
      3_600_000
    );
    expect(particles.map((p) => p.stepId)).toEqual(["in2", "in1"]);
    expect(particles[0].dot).toContain("clay"); // error → 陶土
  });
  it("cap 溢出如实计数", () => {
    const many = Array.from({ length: 30 }, (_, i) =>
      step({ step_id: `s${i}`, ts: at(i * 1000) })
    );
    const { particles, overflow } = particlesInWindow(many, NOW, 3_600_000, 10);
    expect(particles).toHaveLength(10);
    expect(overflow).toBe(20);
  });
  it("坏时间戳的步骤跳过", () => {
    const { particles } = particlesInWindow(
      [step({ step_id: "bad", ts: "not-a-ts" }), step({ step_id: "ok" })],
      NOW,
      3_600_000
    );
    expect(particles.map((p) => p.stepId)).toEqual(["ok"]);
  });
});

describe("coveredSince / coverage", () => {
  it("首步早于窗口起点 = 覆盖完整；晚于 = 不足", () => {
    const full = [step({ step_id: "s0", ts: at(25 * 3_600_000) })];
    const partial = [step({ step_id: "s0", ts: at(3_600_000) })];
    expect(coveredSince(full, NOW, 24 * 3_600_000)).toBe(true);
    expect(coveredSince(partial, NOW, 24 * 3_600_000)).toBe(false);
    expect(coveredSince([], NOW, 3_600_000)).toBe(false);
  });
});

describe("ring14", () => {
  const usageDay = (calls: number) =>
    ({
      rows: 0,
      in_msg: 0,
      out_msg: 0,
      runs: 0,
      reasoning: 0,
      calls,
      in: 0,
      out: 0,
      think: 0,
    }) as import("./types").UsageDay;
  it("14 根柱、今天的键对齐 UTC 日期（台账按 UTC 天分组）", () => {
    const today = new Date(NOW).toISOString().slice(0, 10);
    const days = ring14([[today, usageDay(7)]], new Date(NOW));
    expect(days).toHaveLength(14);
    expect(days[13]).toMatchObject({ day: today, calls: 7 });
    expect(days[0].calls).toBe(0);
  });
});

describe("chaptersOf", () => {
  it("14 天内的章节新→旧；窗口外的不收", () => {
    const recap = {
      episodes: [
        {
          idx: 1,
          title: "旧章",
          summary: "",
          first_ts: at(30 * 86_400_000),
          last_ts: at(29 * 86_400_000),
          n_steps: 5,
          partial: false,
          themes: [],
          notable_steps: [],
        },
        {
          idx: 2,
          title: "新章",
          summary: "剧场 v2 落地",
          first_ts: at(2 * 86_400_000),
          last_ts: at(86_400_000),
          n_steps: 214,
          partial: false,
          themes: [],
          notable_steps: [],
        },
      ],
    } as unknown as Recap;
    const chapters = chaptersOf(recap, NOW);
    expect(chapters.map((c) => c.title)).toEqual(["新章"]);
    expect(chapters[0].nSteps).toBe(214);
  });
});
