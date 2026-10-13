/** 剧场 v2 纯逻辑层（docs/designs/theater.md）：五态判定、舞台分区、
 * 变奏阈值、隔夜账合成、记住偏好。全部纯函数——不碰 fetch、不碰
 * DOM，测试直接覆盖。红线：一切读数从真实数据派生，禁止装饰性假
 * 状态（theater.md §17.1），所以这里的每个字段都有数据来源注释。 */

import type { AgentTask, IdentityActivity, PendingApproval, Usage } from "./types";

/** 心智五态（theater.md §10）：idle 常态 / working 干活 / awaiting
 * 屏息等审批 / error 停滞受伤（陶土 tint）/ cooldown 熔断冷却。 */
export type MindState = "idle" | "working" | "awaiting" | "error" | "cooldown";

/** 五态判定（§10 参数表的派生规则，优先级从高到低）：
 * awaiting（有待批审批——屏息等你拍板）> cooldown（熔断未解）
 * > error（活动停滞——最后一步超龄）> working（调度器在跑）
 * > idle。输入全部来自既有 API 真实字段。 */
export function mindStateOf(input: {
  activity: Pick<IdentityActivity, "state" | "dispatcher_running"> | null;
  pendingApprovals: number;
  coolingUntil: string | null;
  nowMs: number;
}): MindState {
  if (input.pendingApprovals > 0) return "awaiting";
  if (input.coolingUntil && Date.parse(input.coolingUntil) > input.nowMs) {
    return "cooldown";
  }
  if (input.activity?.state === "stalled") return "error";
  if (input.activity?.state === "working" && input.activity.dispatcher_running) {
    return "working";
  }
  return "idle";
}

/** 五态宇宙参数（theater.md §10 表：亮度/转速/呼吸周期/色温覆层）。
 * tint 为 null 表示不加色温覆层；blink 标记覆层呼吸闪烁（等审批的
 * 树脂慢闪）。数值是渲染起点，态间由渲染端阻尼插值过渡。 */
export const MIND_STATE_PARAMS: Record<
  MindState,
  { glow: number; spin: number; hz: number; tint: number | null; blink: boolean }
> = {
  idle: { glow: 0.55, spin: 1, hz: 0.5, tint: null, blink: false },
  working: { glow: 1, spin: 2.2, hz: 1.4, tint: null, blink: false },
  awaiting: { glow: 0.85, spin: 0, hz: 0, tint: 0xd9a441, blink: true },
  error: { glow: 0.55, spin: 1, hz: 0.5, tint: 0xcf6f5a, blink: false },
  cooldown: { glow: 0.35, spin: 0.45, hz: 0.22, tint: null, blink: false },
};

/** 舞台分区制（theater.md §2）：confront 独占中央；其余卡按新近在
 * 前（服务端顺序即到达序），右列至多 maxVisible 张整卡可见，溢出
 * 折叠为聚合 chip。compact（沉淀/变奏降级）单行卡不占整卡配额、
 * 永远可见。 */
export function partitionCards<T extends { id: string }>(
  surfaces: T[],
  isCompact: (s: T) => boolean,
  maxVisible = 3
): { visible: string[]; hiddenCount: number } {
  const full = surfaces.filter((s) => !isCompact(s));
  const visible = full.slice(0, maxVisible).map((s) => s.id);
  return { visible, hiddenCount: full.length - visible.length };
}

/** 变奏阈值（theater.md §16.5）：同 key（kind+标题）第 3 次起降为
 * 单行摘要卡——重复事件动效会退化为注意力税。key 由调用方给
 * （title 优先，缺省回 kind）。返回应折叠的 surface id 集合。 */
export function variationCompactIds(
  surfaces: Array<{ id: string; kind: string; title: string }>
): Set<string> {
  const seen = new Map<string, number>();
  const compact = new Set<string>();
  for (const s of surfaces) {
    const key = `${s.kind}:${s.title}`;
    const n = (seen.get(key) ?? 0) + 1;
    seen.set(key, n);
    if (n >= 3) compact.add(s.id);
  }
  return compact;
}

/** 隔夜账卡合成（theater.md §6）：离开超阈值（默认 4h）且有可说之
 * 事时给账卡。每行都是真实数据：审批取待批队列（超时 = expires
 * 已过），任务取离开期间收口的（updated_at > lastSeen），预算取
 * admission 水位。没有可说之事返回 null——不硬凑一张空卡。 */
export const OVERNIGHT_THRESHOLD_MS = 4 * 60 * 60 * 1000;

export interface OvernightDigest {
  awayMs: number;
  approvalPending: number;
  approvalExpired: number;
  taskDone: AgentTask[];
  budgetRatio: number | null;
  state: IdentityActivity["state"] | null;
}

export function overnightDigestOf(input: {
  lastSeenMs: number;
  nowMs: number;
  approvals: PendingApproval[];
  tasks: AgentTask[];
  usage: Pick<Usage, "admission"> | null;
  activity: Pick<IdentityActivity, "state"> | null;
  thresholdMs?: number;
}): OvernightDigest | null {
  const awayMs = input.nowMs - input.lastSeenMs;
  if (awayMs < (input.thresholdMs ?? OVERNIGHT_THRESHOLD_MS)) return null;
  const now = input.nowMs;
  const pending = input.approvals.filter((a) => Date.parse(a.expires) > now);
  const expired = input.approvals.length - pending.length;
  const taskDone = input.tasks.filter(
    (t) =>
      (t.status === "succeeded" || t.status === "failed") &&
      Date.parse(t.updated_at) > input.lastSeenMs
  );
  const admission = input.usage?.admission;
  const budgetRatio =
    admission && admission.daily_limit > 0
      ? Math.min(1, admission.used_today / admission.daily_limit)
      : null;
  const digest: OvernightDigest = {
    awayMs,
    approvalPending: pending.length,
    approvalExpired: expired,
    taskDone,
    budgetRatio,
    state: input.activity?.state ?? null,
  };
  const empty =
    digest.approvalPending === 0 &&
    digest.approvalExpired === 0 &&
    digest.taskDone.length === 0;
  return empty ? null : digest;
}

/** 记住偏好（theater.md §16.3，approve-and-remember）：按脚本首个
 * 非空行归组（同一命令族的二次审批 prefill 上次决定）。纯键值换
 * 算 + 序列化，存储由调用方注入（localStorage）。 */
export function decisionKeyOf(script: string): string {
  for (const line of script.split("\n")) {
    const trimmed = line.trim();
    if (trimmed) return trimmed.slice(0, 200);
  }
  return "(空脚本)";
}

export type StoredDecision = "approve" | "deny";

export function loadDecision(
  store: { getItem(k: string): string | null },
  identityId: string,
  script: string
): StoredDecision | null {
  const raw = store.getItem(`theater-decision-${identityId}`);
  if (!raw) return null;
  try {
    const map = JSON.parse(raw) as Record<string, StoredDecision>;
    const d = map[decisionKeyOf(script)];
    return d === "approve" || d === "deny" ? d : null;
  } catch {
    return null;
  }
}

export function saveDecision(
  store: { getItem(k: string): string | null; setItem(k: string, v: string): void },
  identityId: string,
  script: string,
  decision: StoredDecision
): void {
  const key = `theater-decision-${identityId}`;
  let map: Record<string, StoredDecision>;
  try {
    map = JSON.parse(store.getItem(key) ?? "{}") as Record<string, StoredDecision>;
  } catch {
    map = {};
  }
  map[decisionKeyOf(script)] = decision;
  store.setItem(key, JSON.stringify(map));
}

/** 等待时长读数（theater.md §16.1）：「已等 47s / 2m 13s / 1h 02m」
 * ——等待时长是「值不值得打断你」的决策输入。 */
export function fmtWaiting(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${String(m % 60).padStart(2, "0")}m`;
}

/** 在场状态（theater.md §8）：本机在座/离开——投影端诚实读数，
 * 只管本标签页看见的「你」，不冒充系统级在场。企微镜像是后端
 * 渠道能力，默认关（§15.3），本类型不携带。 */
export interface Presence {
  away: boolean;
  sinceMs: number;
}
