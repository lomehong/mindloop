import { describe, expect, it } from "vitest";

import { attentionStream } from "./attention";
import type { NormalizedStep } from "./types";

function step(partial: Partial<NormalizedStep> & { step_id: string }): NormalizedStep {
  return {
    ts: "2026-10-15T10:00:00.000Z",
    type: "event",
    source: null,
    preview: "",
    raw: {},
    run_id: null,
    ...partial,
  };
}

describe("attentionStream", () => {
  it("S2 卡/S1 行/S0 折叠按 salience 分级，新→旧", () => {
    const stream = attentionStream([
      step({ step_id: "old", ts: "2026-10-15T08:00:00.000Z", raw: { salience: "s2", source: "git-1", digest: "旧卡" } }),
      step({ step_id: "s2a", ts: "2026-10-15T10:00:00.000Z", raw: { salience: "s2", source: "git-1", digest: "docs/ 有新提交" } }),
      step({ step_id: "s1a", ts: "2026-10-15T09:30:00.000Z", raw: { salience: "s1", source: "web-1", digest: "竞品页更新" } }),
      step({ step_id: "s0a", raw: { salience: "s0", source: "fs-1", digest: "触碰" } }),
      step({ step_id: "s0b", raw: { salience: "s0", source: "fs-1", digest: "触碰2" } }),
      step({ step_id: "non-event", type: "action", raw: { salience: "s2" } }),
    ]);
    expect(stream.s2.map((i) => i.stepId)).toEqual(["s2a", "old"]);
    expect(stream.s1.map((i) => i.stepId)).toEqual(["s1a"]);
    expect(stream.s0Total).toBe(2);
    expect(stream.s0BySource[0]).toEqual({ source: "fs-1", count: 2 });
  });

  it("s3 告警算值得现在看（alert 步骤并入 S2 组）", () => {
    const stream = attentionStream([
      step({ step_id: "a1", type: "alert", raw: { salience: "s3", source: "self", digest: "预算见顶" } }),
    ]);
    expect(stream.s2).toHaveLength(1);
    expect(stream.s2[0].source).toBe("self");
  });

  it("学习期预演：learning-cap 的 s1 按感官计数", () => {
    const stream = attentionStream([
      step({ step_id: "l1", raw: { salience: "s1", source: "file-1", reason: "+learning-cap" } }),
      step({ step_id: "l2", raw: { salience: "s1", source: "file-1", reason: "quiet-held:+learning-cap" } }),
      step({ step_id: "l3", raw: { salience: "s1", source: "web-1", reason: "" } }),
    ]);
    expect(stream.learningCapBySource).toEqual([{ source: "file-1", count: 2 }]);
  });

  it("空窗口：windowStart 为 null", () => {
    expect(attentionStream([]).windowStart).toBeNull();
    expect(attentionStream([]).s0Total).toBe(0);
  });
});
