import { describe, expect, it } from "vitest";

import {
  axisMeters,
  boundaryReadings,
  curvePositionOf,
  CURVE_SEGMENTS,
  legacyEvidence,
  phaseChipOf,
  segmentNameOf,
} from "./growth-dossier";
import { learningCapTotal } from "./attention";
import type { NormalizedStep } from "./types";
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
      learningCapped: 5,
    });
    expect(rows[0]).toMatchObject({ label: "执行授权档", value: "auto · 分级放行（需审批类才等你）" });
    expect(rows[0].hint).toContain("MINDLOOP_EXEC_POLICY");
    expect(rows[0].hint).not.toContain("未显式配置");
    expect(rows[1]).toMatchObject({ label: "自发档日预算", value: "20,000 tokens/日" });
    expect(rows[2]).toMatchObject({ label: "每日总预算", value: "40,000 tokens/日" });
    expect(rows[3].value).toBe("生效中 · 近窗口压制 5 条 → 预演在注意力面");
  });

  it("缺省档标注缺省；未封顶/未配置/robotd 观察模式如实", () => {
    const rows = boundaryReadings({
      env: {},
      dailyLimit: null,
      sensors: {
        sensors: [],
        robotd: { configured: true, mode: "observe" },
      },
      learningCapped: 0,
    });
    expect(rows[0].value).toContain("auto · 分级放行");
    expect(rows[0].hint).toContain("未显式配置");
    expect(rows[0].hint).toContain("缺省 auto");
    expect(rows[1].value).toBe("未配置");
    expect(rows[2].value).toBe("未封顶"); // null = 端点明示不封顶（非缺席）
    // 查询缺席（undefined）才是"读数缺席"
    const absent = boundaryReadings({
      env: {},
      dailyLimit: undefined,
      sensors: { sensors: [] },
      learningCapped: 0,
    });
    expect(absent[2].value).toBe("读数缺席");
    expect(rows[3].value).toBe("机制开启（新感官缺省 3 天）· 窗口内没有被压制的消息");
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

describe("segmentNameOf / phaseChipOf（样张阶段 chip，无百分比）", () => {
  it("投影位置落段：ask→起步期、auto→同行期（边界）、trusted→分身期", () => {
    expect(segmentNameOf(curvePositionOf("ask"))).toBe("起步期");
    expect(segmentNameOf(curvePositionOf("auto"))).toBe("同行期");
    expect(segmentNameOf(curvePositionOf("trusted"))).toBe("分身期");
    expect(segmentNameOf(curvePositionOf("deny"))).toBe("起步期");
  });
  it("chip 只给段名+档位，绝不给百分比", () => {
    const chip = phaseChipOf("auto");
    expect(chip).toBe("同行期 · auto");
    expect(chip).not.toMatch(/\d+\s*%/);
  });
});

describe("axisMeters（样张量条，真实读数填充）", () => {
  it("能力=7 天完成率、信任=档位投影、传承=记忆相对厚度", () => {
    const meters = axisMeters({ tasksDone: 3, tasksTotal: 4, policyMode: "auto", memories: 12 });
    expect(meters.map((m) => m.label)).toEqual(["能力", "信任", "传承"]);
    expect(meters[0].ratio).toBeCloseTo(0.75);
    expect(meters[0].text).toBe("近 7 天 3/4 完成");
    expect(meters[1].ratio).toBeCloseTo(0.4);
    expect(meters[1].text).toBe("auto · 分级放行");
    expect(meters[2].ratio).toBeCloseTo(0.6);
    expect(meters[2].text).toBe("12 条在案");
  });
  it("缺读数画空条但文案诚实", () => {
    const meters = axisMeters({ tasksDone: null, tasksTotal: null, policyMode: null, memories: null });
    expect(meters.every((m) => m.ratio === null)).toBe(true);
    expect(meters.every((m) => m.text === "—")).toBe(true);
  });
  it("零委托与零记忆不是缺读数", () => {
    const meters = axisMeters({ tasksDone: 0, tasksTotal: 0, policyMode: "deny", memories: 0 });
    expect(meters[0].ratio).toBeNull();
    expect(meters[0].text).toBe("近 7 天无委托");
    expect(meters[2].ratio).toBe(0);
    expect(meters[2].text).toBe("0 条在案");
  });
});

describe("learningCapTotal（学习期的轨迹实证口径）", () => {
  const step = (id: string, type: string, raw: Record<string, unknown>) =>
    ({
      step_id: id,
      ts: "2026-10-15T10:00:00.000Z",
      type,
      source: null,
      preview: "",
      raw,
      run_id: null,
    }) as NormalizedStep;
  it("只数带 learning-cap 标记的 event 步骤", () => {
    expect(
      learningCapTotal([
        step("1", "event", { reason: "+learning-cap" }),
        step("2", "event", { reason: "quiet-held:+learning-cap" }),
        step("3", "event", { reason: "first-seen" }),
        step("4", "alert", { reason: "+learning-cap" }), // alert 不算（s3 走告警通道）
      ])
    ).toBe(2);
  });
});
