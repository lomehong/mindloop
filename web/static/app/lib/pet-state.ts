// 桌面宠物（/pet）的纯状态机：从有机体的可观测事实派生表情与播报。
//
// 设计边界（docs/designs/pet.md）：
//   - 有机体没有情绪状态机，宠物也不发明一个——mood 是既有信号
//     （SSE working/step、/activity、/usage admission、llm-health）
//     的纯派生投影，符合"视图皆派生"哲学；
//   - 播报是纯显示层：只消费已经落盘的事实，不触发任何动作；
//   - 「看见≠打扰」由勿扰（手动开关 + 夜间段）兜底：勿扰期间播报
//     不弹泡，只累积为未读，光点带琥珀微点。
//
// 本模块不碰 fetch/DOM/timer——时间一律由调用方以 `now` 传入，
// 便于 vitest 确定性测试；数据获取与装配在 components/pet/pet-app.tsx。

// ---- 心情（mood）：优先级从高到低，先判定者胜 ----

export type PetMood =
  | "offline"
  | "sick"
  | "hungry"
  | "speaking"
  | "alert"
  | "working"
  | "stalled"
  | "dozing"
  | "idle";

export const MOOD_LABEL: Record<PetMood, string> = {
  offline: "未连接",
  sick: "模型不适",
  hungry: "预算吃紧",
  speaking: "正在说话",
  alert: "刚有警情",
  working: "工作中",
  stalled: "停滞",
  dozing: "打盹",
  idle: "悠闲",
};

/** alert/error 步骤后光点保持警情状态时长。 */
export const ALERT_TTL_MS = 90_000;

export interface PetSignals {
  /** web 后端可达（SSE 已建立，或无身份时身份列表可读）。 */
  connected: boolean;
  /** responder 正在说话（SSE status 快照/变化）。 */
  replying: boolean;
  /** 忙碌思考者数（SSE working.busy.length）。 */
  busyThinkers: number;
  /** /activity 的状态；null = 尚无读数。 */
  activity: "working" | "stalled" | "idle" | "asleep" | null;
  /** 最近一步距今秒数（/activity.last_step_age_s）。 */
  lastStepAgeS: number | null;
  /** 今日 token 水位 used_today/daily_limit；null = 未设预算或无读数。 */
  budgetRatio: number | null;
  /** 预算熔断冷却中（admission.cooling_until 未到期）。 */
  cooling: boolean;
  /** 最近一条 alert/error 步骤距今毫秒数；null = 会话内没有见过。 */
  lastAlertAgeMs: number | null;
}

export function deriveMood(s: PetSignals): PetMood {
  if (!s.connected) return "offline";
  if (s.cooling) return "sick";
  if (s.budgetRatio !== null && s.budgetRatio >= 0.8) return "hungry";
  if (s.replying) return "speaking";
  if (s.lastAlertAgeMs !== null && s.lastAlertAgeMs <= ALERT_TTL_MS) {
    return "alert";
  }
  if (s.busyThinkers > 0 || s.activity === "working") return "working";
  if (s.activity === "stalled") return "stalled";
  if (s.activity === "asleep") return "dozing";
  if (s.lastStepAgeS !== null && s.lastStepAgeS > 1800) return "dozing";
  return "idle";
}

// ---- 动效相位：同屏多只时各自错拍（而不是整排同频） ----

/** 稳定相位（0..1，同 seed 恒同值）。种子取身份 id 时：同一身份在
 * 页面各处（masthead/卡/列表）同拍（它们是同一只生物），不同身份
 * 错拍——工作台一屏多只不会像克隆体一样同时呼吸、同时眨眼。 */
export function phaseFor(seed: string): number {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return ((h >>> 0) % 997) / 997;
}

// ---- 微反应：step 类型 → 一闪而过的动作（约 1.6s） ----

export type PetReaction = "ripple" | "focus" | "gaze" | "bloom" | "jolt" | "happy";

/** message→专注、event/taste→涟漪、screen→凝视、final→绽放、
 * alert/error→颤动；reasoning/action/shell-output 等高频工作步骤
 * 不加戏（working mood 已经在表达）。 */
export function reactionForStep(stepType: string): PetReaction | null {
  switch (stepType) {
    case "message":
      return "focus";
    case "event":
    case "taste":
      return "ripple";
    case "screen":
      return "gaze";
    case "final":
      return "bloom";
    case "alert":
    case "error":
      return "jolt";
    default:
      return null;
  }
}

// ---- 勿扰 ----

export interface PetDndConfig {
  /** 手动开关（菜单切换），开 = 全天勿扰。 */
  enabled: boolean;
  /** 夜间段是否生效（默认开，守住"看见≠打扰"）。 */
  night: boolean;
  /** 夜段起止小时（本地时钟）。start > end 表示跨零点。 */
  nightStart: number;
  nightEnd: number;
}

export const DEFAULT_DND: PetDndConfig = {
  enabled: false,
  night: true,
  nightStart: 22,
  nightEnd: 8,
};

export function isDnd(cfg: PetDndConfig, now: number): boolean {
  if (cfg.enabled) return true;
  if (!cfg.night) return false;
  const h = new Date(now).getHours();
  if (cfg.nightStart === cfg.nightEnd) return false;
  if (cfg.nightStart > cfg.nightEnd) {
    return h >= cfg.nightStart || h < cfg.nightEnd;
  }
  return h >= cfg.nightStart && h < cfg.nightEnd;
}

// ---- 播报 ----

export type AnnouncementKind =
  | "alert"
  | "error"
  | "task"
  | "approval"
  | "budget"
  | "breaker"
  | "digest"
  | "info";

export interface PetAnnouncement {
  /** 会话内去重键。 */
  id: string;
  kind: AnnouncementKind;
  text: string;
  ts: number;
}

/** 相邻两条播报的最小间隔（前一条开始展示起算）。 */
export const ANNOUNCE_GAP_MS = 5_000;
/** 单条播报展示时长。 */
export const ANNOUNCE_DISPLAY_MS = 12_000;
/** 未读暂存上限（勿扰期间），超出丢最旧。 */
const UNREAD_CAP = 20;
/** 待播队列上限，超出丢最旧。 */
const QUEUE_CAP = 10;
/** 去重台账上限：超过时丢弃非当日的旧键。 */
const SEEN_CAP = 400;

export function clipText(text: string, max = 80): string {
  const s = text.replace(/\s+/g, " ").trim();
  const r = Array.from(s);
  if (r.length <= max) return s;
  return r.slice(0, max).join("") + "…";
}

export interface PetAnnouncer {
  /** 已受理的播报 id → 受理当日（YYYY-MM-DD），用于会话内去重。 */
  seen: Record<string, string>;
  /** 台账所属日期；跨天清空 dailyKeys。 */
  day: string;
  /** "每日一次"类别（预算水位等）当日已受理的 id。 */
  dailyKeys: Record<string, true>;
  queue: PetAnnouncement[];
  /** 勿扰期间暂存的未读。 */
  unread: PetAnnouncement[];
  current: PetAnnouncement | null;
  currentSince: number;
  lastShownAt: number;
}

function dayKeyOf(now: number): string {
  const d = new Date(now);
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${m}-${day}`;
}

export function createAnnouncer(now = 0): PetAnnouncer {
  return {
    seen: {},
    day: dayKeyOf(now),
    dailyKeys: {},
    queue: [],
    unread: [],
    current: null,
    currentSince: 0,
    lastShownAt: 0,
  };
}

export interface OfferOptions {
  dnd: boolean;
  now: number;
  /** true = 该 id 当日只受理一次（预算水位跨阈值这类重复信号）。 */
  daily?: boolean;
}

/** 受理一条播报：会话内去重 →（可选）当日去重 → 勿扰入未读 / 常态入
 * 队列。重复 id 静默忽略，返回原状态。 */
export function offer(
  a: PetAnnouncer,
  item: PetAnnouncement,
  o: OfferOptions
): PetAnnouncer {
  const day = dayKeyOf(o.now);
  let seen = a.seen;
  let dailyKeys = a.dailyKeys;
  if (a.day !== day) {
    seen = {};
    dailyKeys = {};
  } else if (Object.keys(seen).length > SEEN_CAP) {
    seen = Object.fromEntries(
      Object.entries(seen).filter(([, d]) => d === day)
    );
  }
  if (seen[item.id]) return a;
  if (o.daily && dailyKeys[item.id]) return a;

  const nextSeen = { ...seen, [item.id]: day };
  const nextDaily = o.daily ? { ...dailyKeys, [item.id]: true as const } : dailyKeys;
  if (o.dnd) {
    return {
      ...a,
      seen: nextSeen,
      day,
      dailyKeys: nextDaily,
      unread: [...a.unread, item].slice(-UNREAD_CAP),
    };
  }
  return {
    ...a,
    seen: nextSeen,
    day,
    dailyKeys: nextDaily,
    queue: [...a.queue, item].slice(-QUEUE_CAP),
  };
}

/** 驱动播报展示：到期收起当前条；空闲且距上一条开始 ≥GAP 时从队列
 * 取下一条。每 500ms 调用一次（装配层定时器）。 */
export function tick(a: PetAnnouncer, now: number): PetAnnouncer {
  let next = a;
  if (next.current && now - next.currentSince >= ANNOUNCE_DISPLAY_MS) {
    next = { ...next, current: null };
  }
  if (
    !next.current &&
    next.queue.length > 0 &&
    now - next.lastShownAt >= ANNOUNCE_GAP_MS
  ) {
    const [head, ...rest] = next.queue;
    next = {
      ...next,
      queue: rest,
      current: head,
      currentSince: now,
      lastShownAt: now,
    };
  }
  return next;
}

/** 查看未读：勿扰期间累积的播报移回队首（保持原顺序）。 */
export function drainUnread(a: PetAnnouncer): PetAnnouncer {
  if (a.unread.length === 0) return a;
  return {
    ...a,
    unread: [],
    queue: [...a.unread, ...a.queue].slice(0, QUEUE_CAP),
  };
}

// ---- 播报的构造器（信号 → PetAnnouncement） ----

/** 轨迹步骤 → 播报：alert/error 必播；带 task_id 的 final 播任务完成；
 * 其余步骤不播（S0/S1 沉淀不是播报素材）。 */
export function stepAnnouncement(ev: {
  step_type: string;
  step_id: string;
  excerpt: string;
  task_id: string | null;
}, now: number): PetAnnouncement | null {
  if (ev.step_type === "alert") {
    return { id: `step:${ev.step_id}`, kind: "alert", text: clipText(ev.excerpt), ts: now };
  }
  if (ev.step_type === "error") {
    return { id: `step:${ev.step_id}`, kind: "error", text: clipText(ev.excerpt), ts: now };
  }
  if (ev.step_type === "final" && ev.task_id) {
    return {
      id: `task-final:${ev.step_id}`,
      kind: "task",
      text: `任务完成：${clipText(ev.excerpt)}`,
      ts: now,
    };
  }
  return null;
}

export interface BudgetThreshold {
  key: string;
  ratio: number;
  text: string;
}

const BUDGET_THRESHOLDS: BudgetThreshold[] = [
  { key: "budget:100", ratio: 1, text: "今日预算已用完，自发档已停" },
  { key: "budget:80", ratio: 0.8, text: "今日预算水位过 80%" },
];

/** 预算水位跨阈值：返回当日应播的阈值（daily 去重键 = 阈值键）。
 * daily_limit=0 视为未配置预算，永不播。 */
export function budgetAnnouncements(admission: {
  daily_limit: number;
  used_today: number;
}): BudgetThreshold[] {
  if (admission.daily_limit <= 0) return [];
  const ratio = admission.used_today / admission.daily_limit;
  return BUDGET_THRESHOLDS.filter((t) => ratio >= t.ratio);
}

/** 熔断边沿：cooling_until 出现即播（id 带截止时间，同一次冷却只播
 * 一遍）。 */
export function breakerAnnouncement(
  admission: { cooling_until?: string },
  now: number
): PetAnnouncement | null {
  if (!admission.cooling_until) return null;
  return {
    id: `breaker:${admission.cooling_until}`,
    kind: "breaker",
    text: "模型熔断，冷却中——稍后自动恢复",
    ts: now,
  };
}

/** 审批待办：每份待批脚本一条（id = 审批哈希，已受理不重播）。 */
export function approvalAnnouncement(
  approval: { hash: string; script: string },
  now: number
): PetAnnouncement {
  return {
    id: `approval:${approval.hash}`,
    kind: "approval",
    text: `待审批：${clipText(approval.script, 60)}`,
    ts: now,
  };
}

export interface DigestInput {
  /** 去重键带身份：同一会话内每个身份只播一次开场摘要。 */
  identityId: string;
  identityName: string;
  mood: PetMood;
  budgetText: string;
  pendingApprovals: number;
}

/** 打开宠物时的当日摘要。 */
export function digestAnnouncement(input: DigestInput, now: number): PetAnnouncement {
  const parts = [
    `${input.identityName} · ${MOOD_LABEL[input.mood]}`,
    input.budgetText,
  ];
  if (input.pendingApprovals > 0) {
    parts.push(`待审批 ${input.pendingApprovals} 件`);
  }
  return {
    id: `digest:${input.identityId}`,
    kind: "digest",
    text: parts.join(" · "),
    ts: now,
  };
}
