// 体征带（avatar.md §3.4「生命体征带」）：呼吸/心跳/食量/睡眠/痛觉
// ——顶栏中段常驻，任何分节（含舞台）瞥一眼就知道它活着没有、饿不
// 饿、疼不疼。派生逻辑在 lib/vitals（与桌宠同一份五态信号系，三化身
// 同源）；数据复用 activity/usage/approvals 既有 query key，静默失败
// 退化为不显示该项。命名注意：vital-strip.tsx（单数）是轨迹页的
// 「生命线」柱状图，与本组件无关。

import { useQueries, useQuery } from "@tanstack/react-query";

import { useNowTicker } from "~/lib/use-chat";

import { fetchActivity, fetchApprovals, fetchIdentities, fetchUsage } from "~/lib/api";
import { SLEEP_LABEL, vitalsFor } from "~/lib/vitals";
import type { PetSignals } from "~/lib/pet-state";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import { cn } from "~/lib/utils";

/** 五态 → 光点色：与 need-you/活动徽章同一族色板。 */
const MOOD_DOT: Record<string, string> = {
  offline: "bg-faint",
  sick: "bg-clay",
  awaiting: "bg-resin",
  hungry: "bg-resin",
  speaking: "bg-lake",
  alert: "bg-resin",
  working: "bg-primary",
  stalled: "bg-clay",
  dozing: "bg-faint",
  idle: "bg-moss",
};

function Item({
  label,
  value,
  tone,
  title,
  pulse,
}: {
  label: string;
  value: string;
  tone?: string;
  title?: string;
  pulse?: boolean;
}) {
  return (
    <span className="flex items-center gap-1 whitespace-nowrap" title={title}>
      <span className="text-faint">{label}</span>
      <span className={cn("tabular-nums", tone ?? "text-muted-foreground")}>
        {value}
      </span>
      {pulse && (
        <span className="size-[5px] animate-pulse rounded-full bg-primary" />
      )}
    </span>
  );
}

export function VitalsBand({
  identityId,
  live,
  className,
}: {
  identityId: string;
  /** 未运行的心智没有"此刻体征"——只给 offline 光点，不假装安睡。
   * 缺省自查 identities（与壳共享缓存）。 */
  live?: boolean;
  className?: string;
}) {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30_000,
  });
  const liveHere =
    live ?? identities?.find((it) => it.id === identityId)?.live ?? false;
  const [activityQ, usageQ, approvalsQ] = useQueries({
    queries: [
      {
        queryKey: ["activity", identityId],
        queryFn: () => fetchActivity(identityId),
        enabled: liveHere,
        refetchInterval: STATUS_BACKGROUND_POLL_MS,
      },
      {
        queryKey: ["usage", identityId],
        queryFn: () => fetchUsage(identityId),
        enabled: liveHere,
        refetchInterval: USAGE_IDLE_POLL_MS,
      },
      {
        queryKey: ["approvals", identityId],
        queryFn: () => fetchApprovals(identityId),
        enabled: liveHere,
        refetchInterval: STATUS_BACKGROUND_POLL_MS,
      },
    ],
  });
  // 冷却到期判定需要"现在"——渲染期不裸调 Date.now()（纯性门禁），
  // 用 30s 一拍的 ticker（与轨迹页生命线同一时钟源）。Hook 必须在
  // 早退之前调用（规则要求 unconditional）。
  const nowTick = useNowTicker(liveHere, 30_000);

  if (!liveHere) {
    return (
      <div
        className={cn(
          "items-center gap-3 font-mono text-[11px] text-faint",
          className
        )}
      >
        <span className="flex items-center gap-1">
          <span className="size-[6px] rounded-full bg-faint" />
          未运行
        </span>
      </div>
    );
  }

  const activity = activityQ.data;
  const admission = usageQ.data?.admission;
  const signals: PetSignals = {
    connected: true,
    replying: false, // SSE 专有信号（桌面壳）——体征条不猜
    busyThinkers: activity?.busy_thinkers.length ?? 0,
    activity: activity?.state ?? null,
    lastStepAgeS: activity?.last_step_age_s ?? null,
    budgetRatio:
      admission && admission.daily_limit > 0
        ? admission.used_today / admission.daily_limit
        : null,
    cooling:
      !!admission?.cooling_until &&
      Date.parse(admission.cooling_until) > nowTick,
    pendingApprovals: approvalsQ.data?.length ?? 0,
    lastAlertAgeMs: null,
  };
  const v = vitalsFor(signals);
  return (
    <div
      className={cn(
        "items-center gap-3 font-mono text-[11px]",
        className
      )}
    >
      <span className="flex items-center gap-1" title={`息 · ${v.moodLabel}`}>
        <span
          className={cn(
            "size-[6px] rounded-full",
            MOOD_DOT[v.mood] ?? "bg-faint",
            v.mood === "working" && "animate-pulse"
          )}
        />
        <span className={cn(v.mood === "sick" && "text-clay", v.hungry && "text-resin")}>
          {v.moodLabel}
        </span>
      </span>
      {v.heartbeat !== null && (
        <Item label="心跳" value={`${v.heartbeat} 前`} title="最近一步距今" />
      )}
      {v.appetitePct !== null && (
        <Item
          label="食量"
          value={`${v.appetitePct}%`}
          tone={v.hungry ? "text-resin" : "text-muted-foreground"}
          title={`今日预算水位 ${v.appetitePct}%${v.hungry ? " · 微饿（≥80%）" : ""}`}
        />
      )}
      <Item
        label="睡"
        value={SLEEP_LABEL[v.sleep]}
        tone={v.sleep === "awake" ? "text-muted-foreground" : "text-faint"}
      />
      <Item
        label="痛"
        value={String(v.pain)}
        tone={v.pain > 0 ? "text-clay" : "text-faint"}
        title={v.pain > 0 ? "熔断冷却中" : "无熔断"}
      />
    </div>
  );
}
