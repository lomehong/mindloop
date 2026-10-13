/** 舞台时间感组件（theater.md §6/§7/§8）：在场芯片、工作心跳行、
 * 隔夜账卡。定位是「投影端诚实读数」——全部数据来自既有 API
 * （activity/status/approvals/usage/tasks），没有一项是装饰性假
 * 状态（§17.1 红线）。在场是本机概念：只反映这个标签页看见的
 * 「你」；企微镜像是后端渠道能力，默认关（§15.3），这里不冒充。 */
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";

import {
  fetchActivity,
  fetchApprovals,
  fetchIdentityStatus,
  fetchTasks,
  fetchUsage,
} from "~/lib/api";
import {
  fmtWaiting,
  mindStateOf,
  overnightDigestOf,
  OVERNIGHT_THRESHOLD_MS,
  type MindState,
  type Presence,
} from "~/lib/theater";
import { cn } from "~/lib/utils";
import type { AgentTask, IdentityActivity, PendingApproval, Usage } from "~/lib/types";

const ACTIVITY_POLL_MS = 5000;
const STATUS_POLL_MS = 15000;
const USAGE_POLL_MS = 60000;
const LASTSEEN_TICK_MS = 30000;

function presenceKey(identityId: string): string {
  return `theater-presence-${identityId}`;
}

function readPresence(identityId: string): Presence {
  if (typeof window === "undefined") return { away: false, sinceMs: 0 };
  try {
    const raw = window.localStorage.getItem(presenceKey(identityId));
    if (raw) {
      const p = JSON.parse(raw) as Presence;
      if (typeof p.away === "boolean" && typeof p.sinceMs === "number") return p;
    }
  } catch {
    /* 坏存储当不在场 */
  }
  return { away: false, sinceMs: 0 };
}

function writePresence(identityId: string, p: Presence): void {
  try {
    window.localStorage.setItem(presenceKey(identityId), JSON.stringify(p));
  } catch {
    /* 存储不可用（隐私模式）：在场退化为会话内状态 */
  }
}

/** usePresence：显式在座/离开开关 + 「页面最后可见时刻」追踪。
 * lastVisibleMs 用于隔夜判定——标签页不可见超阈值即「隔夜」，
 * 比"用户离开电脑"更可测（§8 的 honest 版本）。身份切换在渲染期
 * 调整状态（React 官方 reset-on-prop 模式，不经 effect）。 */
export function usePresence(identityId: string): {
  presence: Presence;
  setAway: (away: boolean) => void;
  lastVisibleMs: number;
} {
  const [presence, setPresence] = useState<Presence>(() => readPresence(identityId));
  const [lastVisibleMs, setLastVisibleMs] = useState(() => Date.now());
  const [prevId, setPrevId] = useState(identityId);
  if (prevId !== identityId) {
    setPrevId(identityId);
    setPresence(readPresence(identityId));
  }

  useEffect(() => {
    const touch = () => setLastVisibleMs(Date.now());
    const onVis = () => {
      if (!document.hidden) touch();
    };
    document.addEventListener("visibilitychange", onVis);
    const timer = setInterval(() => {
      if (!document.hidden) touch();
    }, LASTSEEN_TICK_MS);
    return () => {
      document.removeEventListener("visibilitychange", onVis);
      clearInterval(timer);
    };
  }, []);

  const setAway = (away: boolean) => {
    const next: Presence = { away, sinceMs: away ? Date.now() : 0 };
    setPresence(next);
    writePresence(identityId, next);
  };

  return { presence, setAway, lastVisibleMs };
}

/** PresenceChip：舞台右上角「在座 / 离开」（theater.md §8）。 */
export function PresenceChip({
  presence,
  onToggle,
}: {
  presence: Presence;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      data-testid="presence-chip"
      data-away={presence.away}
      onClick={onToggle}
      title={
        presence.away
          ? "离开中：回来时自动合成隔夜账；企微镜像默认关（设置页可开）"
          : "标记离开：inform 及以上事件才会打断你"
      }
      className={cn(
        "flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-[11px] transition-colors",
        presence.away
          ? "border-border text-muted-foreground hover:text-foreground"
          : "border-primary/40 text-foreground hover:text-primary"
      )}
    >
      <span
        className={cn(
          "size-[6px] rounded-full",
          presence.away ? "bg-faint" : "bg-primary"
        )}
      />
      {presence.away ? "离开" : "在座"}
    </button>
  );
}

export interface StageTelemetry {
  activity: IdentityActivity | null;
  stepCount: number | null;
  approvals: PendingApproval[];
  usage: Usage | null;
  tasks: AgentTask[];
  mindState: MindState;
  nowMs: number;
}

/** useStageTelemetry：舞台时间感共用的数据底座（一次轮询，心跳行
 * 与隔夜账卡共用；awaiting 判定也在宇宙层五态里独立轮询，两侧
 * 口径一致、节奏不同——心跳 5s、宇宙 30s，互不依赖）。 */
export function useStageTelemetry(identityId: string): StageTelemetry {
  const activity = useQuery({
    queryKey: ["stage-activity", identityId],
    queryFn: () => fetchActivity(identityId),
    refetchInterval: ACTIVITY_POLL_MS,
  });
  const status = useQuery({
    queryKey: ["stage-status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_POLL_MS,
  });
  const approvals = useQuery({
    queryKey: ["stage-approvals", identityId],
    queryFn: () => fetchApprovals(identityId),
    refetchInterval: ACTIVITY_POLL_MS,
  });
  const usage = useQuery({
    queryKey: ["stage-usage", identityId],
    queryFn: () => fetchUsage(identityId),
    refetchInterval: USAGE_POLL_MS,
  });
  const tasks = useQuery({
    queryKey: ["stage-tasks", identityId],
    queryFn: () => fetchTasks(identityId),
    refetchInterval: STATUS_POLL_MS,
  });
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    // 心跳行的等待时长走秒：仅在场可见时有意义（隐藏期 rAF/轮询暂停）
    const timer = setInterval(() => setNowMs(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  return useMemo(
    () => ({
      activity: activity.data ?? null,
      stepCount: status.data?.step_count ?? null,
      approvals: approvals.data ?? [],
      usage: usage.data ?? null,
      tasks: tasks.data ?? [],
      mindState: mindStateOf({
        activity: activity.data ?? null,
        pendingApprovals: (approvals.data ?? []).length,
        coolingUntil: usage.data?.admission?.cooling_until ?? null,
        nowMs,
      }),
      nowMs,
    }),
    [activity.data, status.data, approvals.data, usage.data, tasks.data, nowMs]
  );
}

const STATE_DOT: Record<MindState, string> = {
  idle: "bg-faint",
  working: "bg-primary animate-pulse",
  awaiting: "bg-resin animate-pulse",
  error: "bg-destructive",
  cooldown: "bg-faint animate-pulse",
};

/** WorkHeartbeat：舞台底部的工作心跳行（theater.md §7）——「它一直
 * 干着」的累积读数。等审批分支带已等待时长（§16.1）。静默卡死
 * 禁令（§17.3）：停滞态本身就是一行可读信号。 */
export function WorkHeartbeat({ tele }: { tele: StageTelemetry }) {
  const { mindState, activity, stepCount, approvals, nowMs } = tele;
  let text: string;
  if (mindState === "awaiting") {
    const oldest = approvals.reduce<number | null>((acc, a) => {
      const c = Date.parse(a.created);
      return acc === null || c < acc ? c : acc;
    }, null);
    text = `等你拍板${oldest !== null ? ` · 已等 ${fmtWaiting(nowMs - oldest)}` : ""} · ${approvals.length} 条待批`;
  } else if (mindState === "working") {
    const run =
      activity?.run_seconds != null ? ` · 本轮 ${fmtWaiting(activity.run_seconds * 1000)}` : "";
    const steps = stepCount != null ? ` · 累计 ${stepCount} 步` : "";
    const busy = activity?.busy_thinkers.length ?? 0;
    text = `工作中${run}${steps} · ${busy} 思考者在途`;
  } else if (mindState === "error") {
    const age = activity?.last_step_age_s;
    text = `停滞 · 最后一步${age != null ? ` ${fmtWaiting(age * 1000)} 前` : ""} · 心跳行停摆是可读信号`;
  } else if (mindState === "cooldown") {
    text = `熔断冷却 · 预算水位回落中${stepCount != null ? ` · 累计 ${stepCount} 步` : ""}`;
  } else {
    text = `待命${stepCount != null ? ` · 累计 ${stepCount} 步` : ""}`;
  }
  return (
    <div
      data-testid="work-heartbeat"
      data-mind-state={mindState}
      className="flex items-center gap-2 font-mono text-[11px] text-muted-foreground"
    >
      <span className={cn("size-[5px] rounded-full", STATE_DOT[mindState])} />
      {text}
    </div>
  );
}

function dayKey(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** OvernightDigest：隔夜账卡（theater.md §6）——标签页不可见（或
 * 显式离开）超阈值时，打开舞台顶部钉一张昨夜战报。dismiss 语义
 * （§15.4）：dismiss 后当天不再合成。 */
export function OvernightDigest({
  identityId,
  tele,
  lastSeenMs,
  onDismiss,
}: {
  identityId: string;
  tele: StageTelemetry;
  lastSeenMs: number;
  onDismiss?: () => void;
}) {
  const navigate = useNavigate();
  const today = dayKey(tele.nowMs);
  const [dismissedDay, setDismissedDay] = useState<string | null>(() => {
    try {
      return window.localStorage.getItem(`theater-digest-dismissed-${identityId}`);
    } catch {
      return null;
    }
  });
  const digest = overnightDigestOf({
    lastSeenMs,
    nowMs: tele.nowMs,
    approvals: tele.approvals,
    tasks: tele.tasks,
    usage: tele.usage,
    activity: tele.activity,
    thresholdMs: OVERNIGHT_THRESHOLD_MS,
  });
  if (!digest || dismissedDay === today) return null;

  const dismiss = () => {
    try {
      window.localStorage.setItem(
        `theater-digest-dismissed-${identityId}`,
        dayKey(tele.nowMs)
      );
    } catch {
      /* 存储不可用：本次会话内仍生效 */
    }
    setDismissedDay(dayKey(tele.nowMs));
    onDismiss?.();
  };

  const hours = Math.floor(digest.awayMs / 3_600_000);
  const minutes = Math.floor((digest.awayMs % 3_600_000) / 60_000);
  return (
    <div
      data-testid="overnight-digest"
      className="w-full max-w-md rounded-xl border border-border bg-card/95 p-4 shadow-lg backdrop-blur-sm"
    >
      <div className="mb-2 flex items-baseline gap-2.5">
        <span className="text-[13px] font-semibold text-foreground">隔夜</span>
        <span className="font-mono text-[10px] text-muted-foreground">
          离开 {hours}h {String(minutes).padStart(2, "0")}m · 其间有活动
        </span>
        <button
          type="button"
          data-testid="digest-dismiss"
          onClick={dismiss}
          title="dismiss 后当天不再合成"
          className="ml-auto text-xs text-muted-foreground hover:text-foreground"
        >
          ✕
        </button>
      </div>
      <div className="space-y-1">
        {digest.approvalPending > 0 && (
          <DigestRow
            n={digest.approvalPending}
            tone="resin"
            text="待批审批在等你"
            action="去处理"
            onAction={() => navigate(`/i/${encodeURIComponent(identityId)}/tasks`)}
          />
        )}
        {digest.approvalExpired > 0 && (
          <DigestRow
            n={digest.approvalExpired}
            tone="resin"
            text="审批已超时（TTL 到期）"
            action="查看任务"
            onAction={() => navigate(`/i/${encodeURIComponent(identityId)}/tasks`)}
          />
        )}
        {digest.taskDone.map((t) => (
          <DigestRow
            key={t.task_id}
            n={t.status === "succeeded" ? 1 : 0}
            tone={t.status === "succeeded" ? "primary" : "destructive"}
            text={`${t.status === "succeeded" ? "任务完成" : "任务失败"}——${t.content.slice(0, 40)}`}
            action="查看轨迹"
            onAction={() =>
              navigate(
                `/i/${encodeURIComponent(identityId)}${t.run_id ? `?step=${encodeURIComponent(t.events[t.events.length - 1]?.step_id ?? "")}` : ""}`
              )
            }
          />
        ))}
        {digest.budgetRatio != null && digest.budgetRatio >= 0.8 && (
          <DigestRow
            n={1}
            tone="resin"
            text={`预算水位 ${Math.round(digest.budgetRatio * 100)}%`}
          />
        )}
      </div>
    </div>
  );
}

function DigestRow({
  n,
  tone,
  text,
  action,
  onAction,
}: {
  n: number;
  tone: "primary" | "resin" | "destructive";
  text: string;
  action?: string;
  onAction?: () => void;
}) {
  const toneCls =
    tone === "primary"
      ? "text-primary"
      : tone === "destructive"
        ? "text-destructive"
        : "text-resin";
  return (
    <div className="flex items-center gap-2.5 py-0.5 text-[12.5px] text-foreground/90">
      <span className={cn("w-4 text-right font-mono text-[15px]", toneCls)}>
        {n}
      </span>
      <span className="min-w-0 flex-1 truncate">{text}</span>
      {action && (
        <button
          type="button"
          onClick={onAction}
          className="shrink-0 text-[11.5px] text-primary hover:underline"
        >
          {action} →
        </button>
      )}
    </div>
  );
}
