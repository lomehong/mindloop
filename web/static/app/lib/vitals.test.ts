import { describe, expect, it } from "vitest";

import { SLEEP_LABEL, vitalsFor } from "./vitals";
import type { PetSignals } from "./pet-state";

function signals(partial: Partial<PetSignals>): PetSignals {
  return {
    connected: true,
    replying: false,
    busyThinkers: 0,
    activity: "idle",
    lastStepAgeS: 120,
    budgetRatio: 0.62,
    cooling: false,
    lastAlertAgeMs: null,
    ...partial,
  };
}

describe("vitalsFor", () => {
  it("常态：清醒 · 不饿 · 不疼，心跳为最近一步", () => {
    const v = vitalsFor(signals({}));
    expect(v.mood).toBe("idle");
    expect(v.moodLabel).toBe("悠闲");
    expect(v.heartbeat).toBe("2m");
    expect(v.appetitePct).toBe(62);
    expect(v.hungry).toBe(false);
    expect(v.sleep).toBe("awake");
    expect(v.pain).toBe(0);
  });

  it("饿是真的饿：水位 ≥80%（与 deriveMood 同判据）", () => {
    const v = vitalsFor(signals({ budgetRatio: 0.81 }));
    expect(v.hungry).toBe(true);
    expect(v.mood).toBe("hungry");
    // 未设预算：不显示水位，也不算饿
    const noBudget = vitalsFor(signals({ budgetRatio: null }));
    expect(noBudget.appetitePct).toBeNull();
    expect(noBudget.hungry).toBe(false);
  });

  it("疼是真的疼：熔断冷却 → sick + 痛 1", () => {
    const v = vitalsFor(signals({ cooling: true, budgetRatio: 0.9 }));
    expect(v.pain).toBe(1);
    expect(v.mood).toBe("sick"); // 疼的优先级高于饿
  });

  it("睡是真的睡：asleep 态 → 安睡；30 分钟无步骤 → 打盹", () => {
    expect(vitalsFor(signals({ activity: "asleep" })).sleep).toBe("asleep");
    expect(vitalsFor(signals({ lastStepAgeS: 1900 })).sleep).toBe("doze");
    expect(vitalsFor(signals({ lastStepAgeS: 1800 })).sleep).toBe("awake");
  });

  it("工作中：忙碌思考者驱动五态", () => {
    const v = vitalsFor(signals({ busyThinkers: 2, activity: "working" }));
    expect(v.mood).toBe("working");
    expect(v.moodLabel).toBe("工作中");
  });

  it("未连接：offline（读数缺席不是安睡）", () => {
    const v = vitalsFor(signals({ connected: false }));
    expect(v.mood).toBe("offline");
  });

  it("睡眠标签词表齐", () => {
    expect(Object.keys(SLEEP_LABEL).sort()).toEqual(["asleep", "awake", "doze"]);
  });
});
