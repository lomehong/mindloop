import { describe, expect, it } from "vitest";

import {
  boundaryReadings,
  curvePositionOf,
  CURVE_SEGMENTS,
  legacyEvidence,
} from "./growth-dossier";
import type { MemoryInfo } from "./types";

describe("CURVE_SEGMENTS", () => {
  it("三段配比与原型一致（40/35/25）", () => {
    expect(CURVE_SEGMENTS.map((s) => s.key)).toEqual(["起步期", "同行期", "分身期"]);
    expect(CURVE_SEGMENTS.reduce((a, s) => a + s.width, 0)).toBeCloseTo(1);
  });
});

describe("curvePositionOf", () => {
  // 投影规则公开可查（avatar.md §10.1：投影不是测量）。
  it("deny/ask 在起步段内，auto 恰在起步→同行边界，trusted 在分身段", () => {
    expect(curvePositionOf("deny")).toBeLessThan(CURVE_SEGMENTS[0].width);
    expect(curvePositionOf("ask")).toBeLessThan(CURVE_SEGMENTS[0].width);
    expect(curvePositionOf("auto")).toBeCloseTo(CURVE_SEGMENTS[0].width);
    expect(curvePositionOf("trusted")).toBeGreaterThan(0.65);
  });
});

describe("boundaryReadings", () => {
  it("授权档/自发预算/总预算/学习期逐行如实", () => {
    const rows = boundaryReadings({
      env: {
        MINDLOOP_EXEC_POLICY: "auto",
        MINDLOOP_SPONTANEOUS_TOKENS: "20000",
      },
      dailyLimit: 40000,
      sensors: {
        sensors: [
          { id: "a", type: "file", learning_days: 3 },
          { id: "b", type: "git" },
        ],
      },
    });
    expect(rows[0]).toMatchObject({ label: "执行授权档", value: "auto · 分级放行（需审批类才等你）" });
    expect(rows[0].hint).toContain("MINDLOOP_EXEC_POLICY");
    expect(rows[0].hint).not.toContain("未显式配置");
    expect(rows[1]).toMatchObject({ label: "自发档日预算", value: "20,000 tokens/日" });
    expect(rows[2]).toMatchObject({ label: "每日总预算", value: "40,000 tokens/日" });
    expect(rows[3].value).toBe("1 只在学习期 · 转正由你点头");
  });

  it("缺省档标注缺省；未封顶/未配置/robotd 观察模式如实", () => {
    const rows = boundaryReadings({
      env: {},
      dailyLimit: 0,
      sensors: {
        sensors: [],
        robotd: { configured: true, mode: "observe" },
      },
    });
    expect(rows[0].value).toContain("auto · 分级放行");
    expect(rows[0].hint).toContain("未显式配置");
    expect(rows[0].hint).toContain("缺省 auto");
    expect(rows[1].value).toBe("未配置");
    expect(rows[2].value).toBe("未封顶");
    expect(rows[3].value).toBe("没有在学习期的感官");
    expect(rows[4]).toMatchObject({ label: "身（robotd）", value: "observe · 只许看不许动" });
  });
});

describe("legacyEvidence", () => {
  const mem = (partial: Partial<MemoryInfo>): MemoryInfo => ({
    name: "m.md",
    mtime: 0,
    id: "1",
    summary: "s",
    type: "preference",
    created: null,
    slug: "m",
    ...partial,
  });

  it("只收 preference 且在案，按 mtime 新→旧", () => {
    const ev = legacyEvidence(
      [
        mem({ name: "old.md", mtime: 1 }),
        mem({ name: "new.md", mtime: 5 }),
        mem({ name: "fact.md", type: "fact" }),
        mem({ name: "dead.md", status: "invalid" }),
      ],
      { Approves: 3, Denies: 1, Undoes: 2 }
    );
    expect(ev.preferences.map((m) => m.name)).toEqual(["new.md", "old.md"]);
    expect(ev.touches).toEqual({ approves: 3, denies: 1, undoes: 2 });
  });

  it("读数缺席就缺席", () => {
    const ev = legacyEvidence(undefined, undefined);
    expect(ev.preferences).toEqual([]);
    expect(ev.touches).toBeNull();
  });
});
