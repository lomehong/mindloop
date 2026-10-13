import { describe, expect, it } from "vitest";

import {
  decisionKeyOf,
  fmtWaiting,
  loadDecision,
  MIND_STATE_PARAMS,
  mindStateOf,
  overnightDigestOf,
  partitionCards,
  saveDecision,
  variationCompactIds,
} from "./theater";
import type { AgentTask, IdentityActivity, PendingApproval } from "./types";

const NOW = Date.parse("2026-10-13T10:00:00Z");

function activity(partial: Partial<IdentityActivity>): IdentityActivity {
  return {
    state: "idle",
    dispatcher_running: false,
    busy_thinkers: [],
    last_step_ts: null,
    last_step_age_s: null,
    run_seconds: null,
    stall_after_s: 900,
    cadence_s: null,
    ...partial,
  };
}

function approval(partial: Partial<PendingApproval>): PendingApproval {
  return {
    hash: "a".repeat(64),
    script: "git push origin main",
    work_dir: "/tmp",
    run_id: "r1",
    created: "2026-10-13T09:00:00Z",
    expires: "2026-10-13T09:30:00Z",
    ...partial,
  };
}

function task(partial: Partial<AgentTask>): AgentTask {
  return {
    task_id: "t1",
    identity_id: "ada",
    from: "op",
    client_message_id: "m1",
    content: "写周报",
    status: "succeeded",
    attempt: 1,
    created_at: "2026-10-13T08:00:00Z",
    updated_at: "2026-10-13T08:30:00Z",
    events: [],
    ...partial,
  };
}

describe("mindStateOf 五态判定", () => {
  it("有待批审批即屏息（awaiting），优先级最高", () => {
    expect(
      mindStateOf({
        activity: activity({ state: "working", dispatcher_running: true }),
        pendingApprovals: 1,
        coolingUntil: null,
        nowMs: NOW,
      })
    ).toBe("awaiting");
  });

  it("熔断未解为冷却（cooldown），优先于停滞与工作", () => {
    expect(
      mindStateOf({
        activity: activity({ state: "stalled" }),
        pendingApprovals: 0,
        coolingUntil: new Date(NOW + 60_000).toISOString(),
        nowMs: NOW,
      })
    ).toBe("cooldown");
  });

  it("活动停滞为出错（error，陶土 tint）", () => {
    expect(
      mindStateOf({
        activity: activity({ state: "stalled" }),
        pendingApprovals: 0,
        coolingUntil: null,
        nowMs: NOW,
      })
    ).toBe("error");
  });

  it("调度器在跑为工作（working）", () => {
    expect(
      mindStateOf({
        activity: activity({ state: "working", dispatcher_running: true }),
        pendingApprovals: 0,
        coolingUntil: null,
        nowMs: NOW,
      })
    ).toBe("working");
  });

  it("熔断过期后回落 idle，不是永远冷却", () => {
    expect(
      mindStateOf({
        activity: activity({ state: "idle" }),
        pendingApprovals: 0,
        coolingUntil: new Date(NOW - 60_000).toISOString(),
        nowMs: NOW,
      })
    ).toBe("idle");
  });

  it("五态都有渲染参数，awaiting 自转停（spin=0）", () => {
    const states = ["idle", "working", "awaiting", "error", "cooldown"] as const;
    for (const s of states) {
      expect(MIND_STATE_PARAMS[s]).toBeDefined();
    }
    expect(MIND_STATE_PARAMS.awaiting.spin).toBe(0);
    expect(MIND_STATE_PARAMS.awaiting.tint).not.toBeNull();
    expect(MIND_STATE_PARAMS.error.tint).not.toBeNull();
    expect(MIND_STATE_PARAMS.idle.tint).toBeNull();
  });
});

describe("partitionCards 舞台分区制", () => {
  const ids = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }, { id: "e" }];

  it("至多 3 张整卡可见，溢出计数", () => {
    const p = partitionCards(ids, () => false, 3);
    expect(p.visible).toEqual(["a", "b", "c"]);
    expect(p.hiddenCount).toBe(2);
  });

  it("compact 单行卡不占整卡配额", () => {
    const p = partitionCards(ids, (s) => s.id === "e", 3);
    expect(p.visible).toEqual(["a", "b", "c"]);
    expect(p.hiddenCount).toBe(1); // d 被折叠；e 是单行不计数
  });

  it("不足 3 张无溢出", () => {
    const p = partitionCards(ids.slice(0, 2), () => false, 3);
    expect(p.hiddenCount).toBe(0);
  });
});

describe("variationCompactIds 变奏阈值", () => {
  it("同类第 3 次起折叠，前两次保留整卡", () => {
    const surfaces = [
      { id: "1", kind: "progress", title: "周报聚合" },
      { id: "2", kind: "progress", title: "周报聚合" },
      { id: "3", kind: "progress", title: "周报聚合" },
      { id: "4", kind: "progress", title: "别的任务" },
    ];
    const compact = variationCompactIds(surfaces);
    expect(compact.has("1")).toBe(false);
    expect(compact.has("2")).toBe(false);
    expect(compact.has("3")).toBe(true);
    expect(compact.has("4")).toBe(false);
  });

  it("不同 kind 同标题不算同类", () => {
    const surfaces = [
      { id: "1", kind: "text", title: "天气" },
      { id: "2", kind: "progress", title: "天气" },
    ];
    expect(variationCompactIds(surfaces).size).toBe(0);
  });
});

describe("overnightDigestOf 隔夜账卡", () => {
  const lastSeen = NOW - 8 * 60 * 60 * 1000; // 离开 8h

  it("离开不足阈值返回 null（不硬凑空卡）", () => {
    expect(
      overnightDigestOf({
        lastSeenMs: NOW - 60 * 60 * 1000,
        nowMs: NOW,
        approvals: [],
        tasks: [],
        usage: null,
        activity: null,
      })
    ).toBeNull();
  });

  it("超阈值但无可说之事也返回 null", () => {
    expect(
      overnightDigestOf({
        lastSeenMs: lastSeen,
        nowMs: NOW,
        approvals: [],
        tasks: [],
        usage: null,
        activity: null,
      })
    ).toBeNull();
  });

  it("审批分待批与超时，任务取离开期间收口的", () => {
    const digest = overnightDigestOf({
      lastSeenMs: lastSeen,
      nowMs: NOW,
      approvals: [
        approval({ expires: new Date(NOW + 600_000).toISOString() }),
        approval({ hash: "b".repeat(64), expires: new Date(NOW - 600_000).toISOString() }),
      ],
      tasks: [
        task({ task_id: "done-1", status: "succeeded", updated_at: new Date(NOW - 3600_000).toISOString() }),
        task({ task_id: "old", status: "succeeded", updated_at: new Date(lastSeen - 1000).toISOString() }),
        task({ task_id: "fail-1", status: "failed", updated_at: new Date(NOW - 1800_000).toISOString() }),
      ],
      usage: { admission: { daily_limit: 2000, used_today: 1500, consecutive_errors: 0, circuit_threshold: 8 } },
      activity: activity({ state: "idle" }),
    });
    expect(digest).not.toBeNull();
    expect(digest!.approvalPending).toBe(1);
    expect(digest!.approvalExpired).toBe(1);
    expect(digest!.taskDone.map((t) => t.task_id)).toEqual(["done-1", "fail-1"]);
    expect(digest!.budgetRatio).toBeCloseTo(0.75);
  });
});

describe("记住偏好（按脚本首行归组）", () => {
  it("同一命令族的二次审批取回上次决定", () => {
    const store = new Map<string, string>();
    const s = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    };
    expect(loadDecision(s, "ada", "git push origin main\n# 注释")).toBeNull();
    saveDecision(s, "ada", "git push origin main\n# 注释", "approve");
    expect(loadDecision(s, "ada", "git push origin main")).toBe("approve");
    expect(loadDecision(s, "ada", "rm -rf /")).toBeNull(); // 不同命令族
    expect(loadDecision(s, "beta", "git push origin main")).toBeNull(); // 不同身份
    saveDecision(s, "ada", "git push origin main", "deny");
    expect(loadDecision(s, "ada", "git push origin main")).toBe("deny"); // 覆盖
  });

  it("坏存储内容安全回退 null", () => {
    const store = { getItem: () => "{broken" };
    expect(loadDecision(store, "ada", "ls")).toBeNull();
  });

  it("decisionKeyOf 取首个非空行", () => {
    expect(decisionKeyOf("\n  \ndocker ps\nmore")).toBe("docker ps");
    expect(decisionKeyOf("")).toBe("(空脚本)");
  });
});

describe("fmtWaiting 等待时长", () => {
  it("秒 / 分秒 / 时分 三段位", () => {
    expect(fmtWaiting(47_000)).toBe("47s");
    expect(fmtWaiting(133_000)).toBe("2m 13s");
    expect(fmtWaiting(3_720_000)).toBe("1h 02m");
    expect(fmtWaiting(-5)).toBe("0s"); // 时钟偏差不打负数
  });
});
