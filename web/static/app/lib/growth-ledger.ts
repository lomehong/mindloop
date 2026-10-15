// 共同成长账（avatar.md §4.0 / §6.2）：同行面第一屏的聚合逻辑层。
// 双向记账——它为你 / 你为它 / 一起长出，只记真实事件，无积分无庆祝
// （avatar.md §8.1）。纯函数：数据全部来自既有 fetch（§0 后端零改动
// 硬边界），时间过滤在客户端按本地时区做。
//
// 机制账（avatar.md §3.2 隐喻机制账）——v1 的三栏对应关系：
//   它为你   = 今日自发行动的收尾（context_kind=autonomous 的 final 步骤；
//              policy 的 decision=auto 审计文件没有 web 端点，代决精确数
//              v1 不呈现，绝不拿自发行动数冒充代决数）；
//   你为它   = 今日味觉步骤（type=taste：approve 放行 / deny 否决 /
//              undo 纠错——审批决定与 undo --because 都落轨迹 taste）；
//   一起长出 = 今日新建记忆（memories.created 落在今日）。
//
// 窗口诚实：轨迹查询是行数窗口（mindlog?tail=N），不是时间窗口——
// windowCoversToday=false（窗口起点落在今日之内）时计数可能偏少，
// 调用方必须如实标注「近 N 步内」，不得写成"今日共 N 件"。

import type { MemoryInfo, NormalizedStep } from "~/lib/types";

export interface LedgerEvent {
  stepId: string;
  ts: string;
  identityId: string;
  identityName: string;
  /** 单行摘要（taste 事件 = 人类可读的动作句，记忆事件 = 条目摘要）。 */
  summary: string;
  /** taste 的 signal（approve/deny/undo）；其余事件无。 */
  signal?: string;
}

export interface IdentityLedger {
  identityId: string;
  identityName: string;
  itForYou: LedgerEvent[];
  youForIt: LedgerEvent[];
  together: LedgerEvent[];
  /** 统计窗口首步的 ts；steps 为空时 null。 */
  windowStart: string | null;
  /** 窗口是否完整覆盖今日（首步 ts 不晚于今日零点）——false 时计数
   * 是"近 N 步内"的下界，展示必须带窗口标注。 */
  windowCoversToday: boolean;
}

/** taste signal → 动作句（与 internal/traj AppendTasteStep 的词表对齐）。 */
export const TASTE_LABEL: Record<string, string> = {
  approve: "放行",
  deny: "否决",
  undo: "纠错",
};

/** 本地时区的 YYYY-MM-DD。步骤 ts 一律 UTC，"今日"按操作员本地日历日。 */
export function localDayKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function dayKeyOfTs(ts: string): string | null {
  const t = Date.parse(ts);
  if (!Number.isFinite(t)) return null;
  return localDayKey(new Date(t));
}

/** 单具身份的成长账：从轨迹窗口与记忆清单里筛出今日事件。 */
export function ledgerFor(input: {
  identityId: string;
  identityName: string;
  steps: NormalizedStep[];
  memories?: MemoryInfo[];
  now?: Date;
}): IdentityLedger {
  const { identityId, identityName, steps } = input;
  const now = input.now ?? new Date();
  const today = localDayKey(now);

  const tag = (stepId: string, ts: string, summary: string, signal?: string): LedgerEvent => ({
    stepId,
    ts,
    identityId,
    identityName,
    summary,
    signal,
  });

  const itForYou: LedgerEvent[] = [];
  const youForIt: LedgerEvent[] = [];
  for (const s of steps) {
    if (dayKeyOfTs(s.ts) !== today) continue;
    if (s.type === "final" && s.raw.context_kind === "autonomous") {
      itForYou.push(tag(s.step_id, s.ts, s.preview));
    } else if (s.type === "taste") {
      const signal = typeof s.raw.signal === "string" ? s.raw.signal : "";
      const cause = typeof s.raw.cause === "string" ? s.raw.cause.trim() : "";
      const label = TASTE_LABEL[signal] ?? signal;
      const summary = cause ? `${label} · ${cause}` : label || "归因反馈";
      youForIt.push(tag(s.step_id, s.ts, summary, signal || undefined));
    }
  }

  const together: LedgerEvent[] = (input.memories ?? [])
    .filter((m) => m.created && dayKeyOfTs(m.created) === today)
    .map((m) => ({
      stepId: m.name,
      ts: m.created as string,
      identityId,
      identityName,
      summary: m.summary || m.type || "新记忆",
    }));

  const startTs = steps.length > 0 ? steps[0].ts : null;
  const firstDay = startTs ? dayKeyOfTs(startTs) : null;
  return {
    identityId,
    identityName,
    itForYou: newestFirst(itForYou),
    youForIt: newestFirst(youForIt),
    together: newestFirst(together),
    windowStart: startTs,
    // 窗口首步还在今日之内 → 今日更早的事件可能在窗口之外。
    windowCoversToday: firstDay !== null && firstDay !== today,
  };
}

function newestFirst(list: LedgerEvent[]): LedgerEvent[] {
  return [...list].sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts));
}

/** 多具身份合账：三栏各按时间新→旧。 */
export function mergeLedgers(list: IdentityLedger[]): {
  itForYou: LedgerEvent[];
  youForIt: LedgerEvent[];
  together: LedgerEvent[];
} {
  return {
    itForYou: newestFirst(list.flatMap((l) => l.itForYou)),
    youForIt: newestFirst(list.flatMap((l) => l.youForIt)),
    together: newestFirst(list.flatMap((l) => l.together)),
  };
}

// ---- 三轴读数（avatar.md §4.2：能力/信任/传承——全是既有机制读数） ----

export interface AxisFacts {
  /** 技能数（手艺）；null = 读数缺席。 */
  skills: number | null;
  /** 近 7 天显式委托：完成 / 收尾（succeeded+failed）。null = 缺席。 */
  tasksDone: number | null;
  tasksTotal: number | null;
  /** 执行授权档；policyDefaulted=true 表示 .env 未显式配置（缺省 auto）。 */
  policyMode: "auto" | "ask" | "trusted" | "deny" | null;
  policyDefaulted: boolean;
  /** 今日预算水位（usage.admission）；limit=0 表示未封顶。 */
  admission: { used: number; limit: number } | null;
  /** 自发档日预算 tokens（MINDLOOP_SPONTANEOUS_TOKENS）；null = 未配置。 */
  spontaneous: number | null;
  /** 在案记忆条数。 */
  memories: number | null;
  /** 近 7 天味觉聚合（obs.TasteSummary）。 */
  taste7d: { approves: number; denies: number; undoes: number } | null;
}

/** 与 internal/policy ParseMode 同规则：空值落缺省 auto，拼错落保守
 * ask——前端解读不能比后端宽。 */
export function parsePolicyMode(raw: string | undefined): {
  mode: AxisFacts["policyMode"];
  defaulted: boolean;
} {
  const v = (raw ?? "").trim().toLowerCase();
  if (v === "") return { mode: "auto", defaulted: true };
  if (v === "auto") return { mode: "auto", defaulted: false };
  if (v === "ask" || v === "trusted" || v === "deny") return { mode: v, defaulted: false };
  return { mode: "ask", defaulted: false };
}

const POLICY_TEXT: Record<NonNullable<AxisFacts["policyMode"]>, string> = {
  auto: "auto 档 · 分级放行",
  ask: "ask 档 · 全审",
  trusted: "trusted 档 · 直执行",
  deny: "deny 档 · 只思考",
};

/** 三轴一句话读数。缺读数就说缺（"—"），绝不编数（avatar.md §8.1）。 */
export function axisLines(f: AxisFacts): {
  ability: string;
  trust: string;
  legacy: string;
} {
  const ability =
    f.skills === null
      ? null
      : `手艺 ${f.skills} 门${
          f.tasksTotal !== null && f.tasksTotal > 0
            ? ` · 近 7 天委托 ${f.tasksDone ?? 0}/${f.tasksTotal} 完成`
            : ""
        }`;

  const trustParts: string[] = [];
  if (f.policyMode) trustParts.push(POLICY_TEXT[f.policyMode]);
  if (f.spontaneous !== null) trustParts.push(`自发 ${compactTokens(f.spontaneous)}/日`);
  if (f.admission && f.admission.limit > 0) {
    const pct = Math.round((f.admission.used / f.admission.limit) * 100);
    trustParts.push(`水位 ${pct}%`);
  }
  const trust = trustParts.length > 0 ? trustParts.join(" · ") : null;

  const legacyParts: string[] = [];
  if (f.memories !== null) legacyParts.push(`记忆 ${f.memories} 条在案`);
  if (f.taste7d) {
    const touches = f.taste7d.approves + f.taste7d.denies + f.taste7d.undoes;
    if (touches > 0) legacyParts.push(`近 7 天你过手 ${touches} 次`);
  }
  const legacy = legacyParts.length > 0 ? legacyParts.join(" · ") : null;

  return {
    ability: ability ?? "手艺读数缺席",
    trust: trust ?? "授权档读数缺席",
    legacy: legacy ?? "还没有记录在案的传承",
  };
}

/** token 数缩写（12,400 → 12.4k）——读数条空间有限，与 formatCount
 * 不同的是这里用于紧凑轴行。 */
export function compactTokens(n: number): string {
  if (n >= 1_000_000) return `${trim1(n / 1_000_000)}M`;
  if (n >= 1_000) return `${trim1(n / 1_000)}k`;
  return String(n);
}

function trim1(v: number): string {
  const s = v.toFixed(1);
  return s.endsWith(".0") ? s.slice(0, -2) : s;
}
