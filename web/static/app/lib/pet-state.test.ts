import { describe, expect, it } from "vitest";

import {
  ALERT_TTL_MS,
  ANNOUNCE_DISPLAY_MS,
  ANNOUNCE_GAP_MS,
  approvalAnnouncement,
  breakerAnnouncement,
  budgetAnnouncements,
  clipText,
  createAnnouncer,
  DEFAULT_DND,
  deriveMood,
  digestAnnouncement,
  drainUnread,
  isDnd,
  offer,
  reactionForStep,
  stepAnnouncement,
  tick,
  type PetSignals,
} from "~/lib/pet-state";

const baseSignals: PetSignals = {
  connected: true,
  replying: false,
  busyThinkers: 0,
  activity: "idle",
  lastStepAgeS: 60,
  budgetRatio: 0.1,
  cooling: false,
  lastAlertAgeMs: null,
};

describe("deriveMood 优先级", () => {
  it("offline 压过一切", () => {
    expect(
      deriveMood({ ...baseSignals, connected: false, replying: true, cooling: true })
    ).toBe("offline");
  });

  it("熔断冷却（sick）压过预算与说话", () => {
    expect(
      deriveMood({ ...baseSignals, cooling: true, budgetRatio: 0.9, replying: true })
    ).toBe("sick");
  });

  it("预算 ≥80%（hungry）压过说话", () => {
    expect(
      deriveMood({ ...baseSignals, budgetRatio: 0.8, replying: true })
    ).toBe("hungry");
  });

  it("预算 79.9% 不算 hungry", () => {
    expect(deriveMood({ ...baseSignals, budgetRatio: 0.799 })).toBe("idle");
  });

  it("speaking 压过 alert/working", () => {
    expect(
      deriveMood({
        ...baseSignals,
        replying: true,
        lastAlertAgeMs: 1000,
        busyThinkers: 2,
      })
    ).toBe("speaking");
  });

  it("近期 alert 步骤 → alert，超过 TTL 后回落", () => {
    expect(deriveMood({ ...baseSignals, lastAlertAgeMs: 1000 })).toBe("alert");
    expect(
      deriveMood({ ...baseSignals, lastAlertAgeMs: ALERT_TTL_MS + 1 })
    ).toBe("idle");
  });

  it("working：SSE busy 或 /activity 任一为 working", () => {
    expect(deriveMood({ ...baseSignals, busyThinkers: 1 })).toBe("working");
    expect(deriveMood({ ...baseSignals, activity: "working" })).toBe("working");
  });

  it("stalled 与 dozing（asleep / 超半小时无步骤）", () => {
    expect(deriveMood({ ...baseSignals, activity: "stalled" })).toBe("stalled");
    expect(deriveMood({ ...baseSignals, activity: "asleep" })).toBe("dozing");
    expect(deriveMood({ ...baseSignals, lastStepAgeS: 1801 })).toBe("dozing");
  });

  it("无读数时默认 idle", () => {
    expect(
      deriveMood({
        ...baseSignals,
        activity: null,
        lastStepAgeS: null,
        budgetRatio: null,
      })
    ).toBe("idle");
  });
});

describe("reactionForStep 映射", () => {
  it("五类映射与高频步骤不加戏", () => {
    expect(reactionForStep("message")).toBe("focus");
    expect(reactionForStep("event")).toBe("ripple");
    expect(reactionForStep("taste")).toBe("ripple");
    expect(reactionForStep("screen")).toBe("gaze");
    expect(reactionForStep("final")).toBe("bloom");
    expect(reactionForStep("alert")).toBe("jolt");
    expect(reactionForStep("error")).toBe("jolt");
    expect(reactionForStep("reasoning")).toBeNull();
    expect(reactionForStep("action")).toBeNull();
    expect(reactionForStep("shell-output")).toBeNull();
  });
});

describe("播报去重与队列", () => {
  const item = (id: string) => ({ id, kind: "info" as const, text: id, ts: 1000 });

  it("同 id 只受理一次", () => {
    let a = createAnnouncer(1000);
    a = offer(a, item("x"), { dnd: false, now: 1000 });
    a = offer(a, item("x"), { dnd: false, now: 2000 });
    expect(a.queue).toHaveLength(1);
  });

  it("daily 播报当日只受理一次，跨天重置", () => {
    const morning = new Date("2026-10-04T09:00:00").getTime();
    const evening = new Date("2026-10-04T21:00:00").getTime();
    const nextDay = new Date("2026-10-05T09:00:00").getTime();
    let a = createAnnouncer(morning);
    a = offer(a, item("budget:80"), { dnd: false, now: morning, daily: true });
    a = offer(a, item("budget:80"), { dnd: false, now: evening, daily: true });
    expect(a.queue).toHaveLength(1);
    a = offer(a, item("budget:80"), { dnd: false, now: nextDay, daily: true });
    expect(a.queue).toHaveLength(2);
  });

  it("勿扰期间入未读而非队列，查看未读移回队首", () => {
    let a = createAnnouncer(1000);
    a = offer(a, item("a"), { dnd: true, now: 1000 });
    a = offer(a, item("b"), { dnd: true, now: 1000 });
    expect(a.queue).toHaveLength(0);
    expect(a.unread.map((u) => u.id)).toEqual(["a", "b"]);
    a = drainUnread(a);
    expect(a.unread).toHaveLength(0);
    expect(a.queue.map((q) => q.id)).toEqual(["a", "b"]);
  });

  it("tick 按 GAP 出队、按 DISPLAY 收起", () => {
    const t0 = 1_000_000;
    let a = createAnnouncer(t0);
    a = offer(a, item("a"), { dnd: false, now: t0 });
    a = offer(a, item("b"), { dnd: false, now: t0 });
    // 首条立即出队（真实时钟下 lastShownAt=0 早已满足 GAP）。
    a = tick(a, t0);
    expect(a.current?.id).toBe("a");
    // 展示期内不再出队。
    a = tick(a, t0 + ANNOUNCE_GAP_MS);
    expect(a.current?.id).toBe("a");
    // 到期收起，且距上一条开始 ≥GAP 后取下一条。
    a = tick(a, t0 + ANNOUNCE_DISPLAY_MS + ANNOUNCE_GAP_MS);
    expect(a.current?.id).toBe("b");
  });

  it("台账超限时丢弃非当日旧键，当日键保留去重", () => {
    const now = new Date("2026-10-04T12:00:00").getTime();
    let a = createAnnouncer(now);
    for (let i = 0; i < 401; i++) {
      a = offer(a, item(`old-${i}`), { dnd: true, now });
      // 台账里的条目标成"昨日"：直接改写 day 模拟跨天累积。
      a.seen[`old-${i}`] = "2026-10-03";
    }
    a = offer(a, item("today"), { dnd: false, now });
    expect(a.seen["today"]).toBeDefined();
    expect(a.seen["old-0"]).toBeUndefined();
  });
});

describe("勿扰判定", () => {
  const at = (h: number) => new Date(2026, 9, 4, h, 0, 0).getTime();

  it("手动开关优先", () => {
    expect(isDnd({ ...DEFAULT_DND, enabled: true }, at(12))).toBe(true);
  });

  it("夜间段默认 22:00–08:00 跨零点", () => {
    expect(isDnd(DEFAULT_DND, at(23))).toBe(true);
    expect(isDnd(DEFAULT_DND, at(3))).toBe(true);
    expect(isDnd(DEFAULT_DND, at(7))).toBe(true);
    expect(isDnd(DEFAULT_DND, at(8))).toBe(false);
    expect(isDnd(DEFAULT_DND, at(12))).toBe(false);
    expect(isDnd(DEFAULT_DND, at(22))).toBe(true);
  });

  it("关掉夜间段则只看手动开关", () => {
    expect(isDnd({ ...DEFAULT_DND, night: false }, at(23))).toBe(false);
  });
});

describe("播报构造器", () => {
  const now = 1_000_000;

  it("alert/error/final(带 task_id) 播，其余不播", () => {
    const ev = {
      step_type: "alert",
      step_id: "s1",
      excerpt: "预算告警",
      task_id: null,
    };
    expect(stepAnnouncement(ev, now)?.kind).toBe("alert");
    expect(
      stepAnnouncement({ ...ev, step_type: "error" }, now)?.kind
    ).toBe("error");
    expect(
      stepAnnouncement({ ...ev, step_type: "final", task_id: "t1" }, now)?.kind
    ).toBe("task");
    expect(stepAnnouncement({ ...ev, step_type: "final" }, now)).toBeNull();
    expect(stepAnnouncement({ ...ev, step_type: "reasoning" }, now)).toBeNull();
  });

  it("预算阈值：未配置不播，跨 80% 与 100% 分别命中", () => {
    expect(budgetAnnouncements({ daily_limit: 0, used_today: 999 })).toEqual([]);
    expect(
      budgetAnnouncements({ daily_limit: 100, used_today: 50 })
    ).toEqual([]);
    expect(
      budgetAnnouncements({ daily_limit: 100, used_today: 85 }).map((b) => b.key)
    ).toEqual(["budget:80"]);
    expect(
      budgetAnnouncements({ daily_limit: 100, used_today: 100 }).map((b) => b.key)
    ).toEqual(["budget:100", "budget:80"]);
  });

  it("熔断只在 cooling_until 出现时播，同一次冷却 id 稳定", () => {
    expect(breakerAnnouncement({}, now)).toBeNull();
    const a = breakerAnnouncement({ cooling_until: "2026-10-04T12:00:00Z" }, now);
    expect(a?.id).toBe("breaker:2026-10-04T12:00:00Z");
    expect(a).toEqual(
      breakerAnnouncement({ cooling_until: "2026-10-04T12:00:00Z" }, now)
    );
  });

  it("审批与摘要去重键", () => {
    expect(approvalAnnouncement({ hash: "h1", script: "rm -rf /" }, now).id).toBe(
      "approval:h1"
    );
    const d = digestAnnouncement(
      {
        identityId: "ada",
        identityName: "ada",
        mood: "idle",
        budgetText: "未设预算",
        pendingApprovals: 2,
      },
      now
    );
    expect(d.id).toBe("digest:ada");
    expect(d.text).toContain("待审批 2 件");
  });
});

describe("clipText", () => {
  it("折叠空白并截断", () => {
    expect(clipText("  a\n  b  ")).toBe("a b");
    expect(clipText("x".repeat(81))).toBe("x".repeat(80) + "…");
    // CJK 按 rune 计数，不按 UTF-16 码元。
    expect(clipText("好".repeat(81), 80)).toHaveLength(81);
  });
});
