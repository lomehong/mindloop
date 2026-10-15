import { describe, expect, it } from "vitest";

import {
  axisLines,
  compactTokens,
  ledgerFor,
  localDayKey,
  mergeLedgers,
  parsePolicyMode,
  TASTE_LABEL,
  type AxisFacts,
} from "./growth-ledger";
import type { MemoryInfo, NormalizedStep } from "./types";

// now 钉在本地正午：步骤 ts 全部相对它按本地时刻构造，"今日"判定
// 在任何时区（本机 +08 / CI ubuntu +00）语义一致。
const NOW = (() => {
  const d = new Date();
  d.setHours(12, 0, 0, 0);
  return d;
})();

/** 本地今日 h 点整的 UTC ISO 串。 */
function todayTs(h: number): string {
  const d = new Date(NOW);
  d.setHours(h, 0, 0, 0);
  return d.toISOString();
}

/** 本地昨日 h 点整的 UTC ISO 串。 */
function yesterdayTs(h = 10): string {
  const d = new Date(NOW);
  d.setDate(d.getDate() - 1);
  d.setHours(h, 0, 0, 0);
  return d.toISOString();
}

function step(partial: Partial<NormalizedStep> & { step_id: string }): NormalizedStep {
  return {
    ts: todayTs(10),
    type: "final",
    source: null,
    preview: "",
    raw: {},
    run_id: null,
    ...partial,
  };
}

function memory(partial: Partial<MemoryInfo>): MemoryInfo {
  return {
    name: "mem-1",
    mtime: 0,
    id: "1",
    summary: "sum",
    type: "preference",
    created: todayTs(10),
    slug: "mem-1",
    ...partial,
  };
}

describe("localDayKey", () => {
  it("按本地时区取日历日", () => {
    const noon = new Date(NOW);
    const midnight = new Date(NOW);
    midnight.setHours(0, 0, 0, 0);
    const tomorrow = new Date(midnight);
    tomorrow.setDate(tomorrow.getDate() + 1);
    expect(localDayKey(noon)).toBe(localDayKey(midnight));
    expect(localDayKey(noon)).not.toBe(localDayKey(tomorrow));
  });
});

describe("ledgerFor", () => {
  it("它为你：只收 autonomous 的 final 步骤，普通运行收尾不算", () => {
    const ledger = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [
        step({ step_id: "f1", type: "final", raw: { context_kind: "autonomous" }, preview: "自己修好了 CI 红灯" }),
        step({ step_id: "f2", type: "final", raw: {}, preview: "任务收尾" }),
        step({ step_id: "a1", type: "action", raw: { context_kind: "autonomous" }, preview: "中途结论" }),
      ],
    });
    expect(ledger.itForYou.map((e) => e.stepId)).toEqual(["f1"]);
    expect(ledger.itForYou[0].summary).toBe("自己修好了 CI 红灯");
  });

  it("你为它：taste 步骤带 signal 与 cause，昨日的不算", () => {
    const ledger = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [
        step({ step_id: "t1", type: "taste", raw: { signal: "undo", cause: "提案多余" } }),
        step({ step_id: "t2", type: "taste", raw: { signal: "approve" } }),
        step({ step_id: "t3", type: "taste", ts: yesterdayTs(), raw: { signal: "deny" } }),
      ],
    });
    expect(ledger.youForIt.map((e) => e.stepId)).toEqual(["t1", "t2"]);
    expect(ledger.youForIt[0].summary).toBe("纠错 · 提案多余");
    expect(ledger.youForIt[1].summary).toBe("放行");
    expect(ledger.youForIt[0].signal).toBe("undo");
  });

  it("一起长出：只收 created 在今日的记忆", () => {
    const ledger = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [],
      memories: [
        memory({ name: "new-one", summary: "commit 备注用中文" }),
        memory({ name: "old", created: yesterdayTs() }),
        memory({ name: "no-ts", created: null }),
      ],
    });
    expect(ledger.together).toHaveLength(1);
    expect(ledger.together[0].summary).toBe("commit 备注用中文");
  });

  it("窗口诚实：首步落在今日之内时 windowCoversToday=false", () => {
    const covered = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [step({ step_id: "s0", ts: yesterdayTs(20) })],
    });
    const uncovered = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [step({ step_id: "s0", ts: todayTs(11) })],
    });
    expect(covered.windowCoversToday).toBe(true);
    expect(uncovered.windowCoversToday).toBe(false);
    expect(ledgerFor({ identityId: "a", identityName: "a", now: NOW, steps: [] }).windowStart).toBeNull();
  });

  it("事件按时间新→旧", () => {
    const ledger = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [
        step({ step_id: "early", ts: todayTs(8), raw: { context_kind: "autonomous" } }),
        step({ step_id: "late", ts: todayTs(14), raw: { context_kind: "autonomous" } }),
      ],
    });
    expect(ledger.itForYou.map((e) => e.stepId)).toEqual(["late", "early"]);
  });
});

describe("mergeLedgers", () => {
  it("多身份合账按时间排序并保留身份署名", () => {
    const a = ledgerFor({
      identityId: "ada",
      identityName: "ada",
      now: NOW,
      steps: [step({ step_id: "a1", ts: todayTs(9), raw: { context_kind: "autonomous" } })],
    });
    const b = ledgerFor({
      identityId: "kuku",
      identityName: "kuku",
      now: NOW,
      steps: [step({ step_id: "b1", ts: todayTs(13), raw: { context_kind: "autonomous" } })],
    });
    const merged = mergeLedgers([a, b]);
    expect(merged.itForYou.map((e) => e.stepId)).toEqual(["b1", "a1"]);
    expect(merged.itForYou[0].identityName).toBe("kuku");
  });
});

describe("parsePolicyMode", () => {
  // 与 internal/policy ParseMode 同规则（avatar.md §0 配置拼写错误
  // 绝不静默放宽——前端解读不能比后端宽）。
  it("空值落缺省 auto 且标 defaulted", () => {
    expect(parsePolicyMode(undefined)).toEqual({ mode: "auto", defaulted: true });
    expect(parsePolicyMode("")).toEqual({ mode: "auto", defaulted: true });
  });
  it("识别四档；拼错落保守 ask", () => {
    expect(parsePolicyMode("auto")).toEqual({ mode: "auto", defaulted: false });
    expect(parsePolicyMode(" ASK ")).toEqual({ mode: "ask", defaulted: false });
    expect(parsePolicyMode("trusted")).toEqual({ mode: "trusted", defaulted: false });
    expect(parsePolicyMode("deny")).toEqual({ mode: "deny", defaulted: false });
    expect(parsePolicyMode("aoto")).toEqual({ mode: "ask", defaulted: false });
  });
});

describe("axisLines", () => {
  const full: AxisFacts = {
    skills: 7,
    tasksDone: 5,
    tasksTotal: 6,
    policyMode: "auto",
    policyDefaulted: true,
    admission: { used: 12400, limit: 20000 },
    spontaneous: 20000,
    memories: 12,
    taste7d: { approves: 5, denies: 1, undoes: 2 },
  };
  it("三轴组装真实读数", () => {
    const lines = axisLines(full);
    expect(lines.ability).toBe("手艺 7 门 · 近 7 天委托 5/6 完成");
    expect(lines.trust).toBe("auto 档 · 分级放行 · 自发 20k/日 · 水位 62%");
    expect(lines.legacy).toBe("记忆 12 条在案 · 近 7 天你过手 8 次");
  });
  it("缺读数就说缺，不编数", () => {
    const lines = axisLines({
      skills: null,
      tasksDone: null,
      tasksTotal: null,
      policyMode: null,
      policyDefaulted: true,
      admission: null,
      spontaneous: null,
      memories: 0,
      taste7d: { approves: 0, denies: 0, undoes: 0 },
    });
    expect(lines.ability).toBe("手艺读数缺席");
    expect(lines.trust).toBe("授权档读数缺席");
    // 记忆 0 条是真实读数，可以展示；过手 0 次不展示（常态，非信号）。
    expect(lines.legacy).toBe("记忆 0 条在案");
  });
  it("未封顶预算不显示水位", () => {
    const lines = axisLines({ ...full, admission: { used: 500, limit: 0 }, spontaneous: null });
    expect(lines.trust).toBe("auto 档 · 分级放行");
  });
});

describe("compactTokens", () => {
  it("k/M 缩写", () => {
    expect(compactTokens(980)).toBe("980");
    expect(compactTokens(20000)).toBe("20k");
    expect(compactTokens(12400)).toBe("12.4k");
    expect(compactTokens(2_500_000)).toBe("2.5M");
  });
});

describe("TASTE_LABEL", () => {
  it("词表与轨迹 AppendTasteStep 对齐", () => {
    expect(Object.keys(TASTE_LABEL).sort()).toEqual(["approve", "deny", "undo"]);
  });
});
