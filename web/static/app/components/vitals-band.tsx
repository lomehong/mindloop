// 体征带（avatar.md §3.4「生命体征带」，样张 #vitals 形态）：固定
// 底部居中 pill，任何分节（含舞台）瞥一眼就知道它活着没有、饿不饿、
// 疼不疼。派生逻辑在 lib/vitals（与桌宠同一份五态信号系，三化身同
// 源）；数据复用 activity/usage/approvals 既有 query key，静默失败
// 退化为不显示该项。命名注意：vital-strip.tsx（单数）是轨迹页的
// 「生命线」柱状图，与本组件无关。

import { useQueries, useQuery } from "@tanstack/react-query";

import { fetchActivity, fetchApprovals, fetchIdentities, fetchUsage } from "~/lib/api";
import { useNowTicker } from "~/lib/use-chat";
import { SLEEP_LABEL, vitalsFor } from "~/lib/vitals";
import { usePresence } from "~/components/stage-hud";
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
  children,
  title,
}: {
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <span className="flex items-center gap-1.5 whitespace-nowrap" title={title}>
      {children}
    </span>
  );
}

const gap = <span className="h-3.5 w-px shrink-0 bg-line-strong" />;

export function VitalsBand({
  identityId,
  live,
  align = "center",
  className,
}: {
  identityId: string;
  /** 未运行的心智没有"此刻体征"——只给 offline 光点，不假装安睡。
   * 缺省自查 identities（与壳共享缓存）。 */
  live?: boolean;
  /** 舞台传 "right"：底部对话条占居中位，pill 让到右下角。 */
  align?: "center" | "right";
  className?: string;
}) {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30_000,
  });
  const me = identities?.find((it) => it.id === identityId);
  const liveHere = live ?? me?.live ?? false;
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
  // 用 30s 一拍的 ticker（与轨迹页生命线同一时钟源）。Hook 必须
  // unconditional——在早退之前调用。
  const nowTick = useNowTicker(liveHere, 30_000);
  const { presence, setAway } = usePresence(identityId);
  const port = typeof window !== "undefined" ? window.location.port : "";

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
        "pointer-events-auto fixed bottom-3.5 z-[60] flex items-center gap-3 rounded-full border border-line-strong bg-card/95 py-2 pl-4 pr-5 font-mono text-[11.5px] text-muted-foreground shadow-[0_4px_24px_rgba(0,0,0,0.35)] backdrop-blur",
        align === "center" ? "left-1/2 -translate-x-1/2" : "right-6",
        className
      )}
    >
      <Item title={`息 · ${v.moodLabel}`}>
        <span
          className={cn(
            "size-2 rounded-full",
            MOOD_DOT[v.mood] ?? "bg-faint",
            v.mood === "working" && "animate-pulse"
          )}
        />
        <span className={cn(v.mood === "sick" && "text-clay", v.hungry && "text-resin")}>
          {me?.name ?? identityId} {v.moodLabel}
        </span>
      </Item>
      {gap}
      {v.heartbeat !== null ? (
        <Item title="最近一步距今">
          心跳 <b className="font-normal text-foreground">{v.heartbeat}</b>
        </Item>
      ) : (
        <Item>心跳 —</Item>
      )}
      {gap}
      {v.appetitePct !== null ? (
        <Item title={`今日预算水位 ${v.appetitePct}%${v.hungry ? " · 微饿（≥80%）" : ""}`}>
          食量{" "}
          <b className={cn("font-normal", v.hungry ? "text-resin" : "text-foreground")}>
            {v.appetitePct}%
          </b>
        </Item>
      ) : (
        <Item title="未设预算">食量 —</Item>
      )}
      {gap}
      <Item title={v.sleep === "awake" ? "清醒" : SLEEP_LABEL[v.sleep]}>
        {SLEEP_LABEL[v.sleep]}
      </Item>
      {gap}
      <Item title={v.pain > 0 ? "熔断冷却中" : "无熔断"}>
        痛觉{" "}
        <b className={cn("font-normal", v.pain > 0 ? "text-clay" : "text-foreground")}>
          {v.pain}
        </b>
      </Item>
      {gap}
      {/* 在座芯片（theater §8）：点击切换；离开=inform 镜像推企微（默认关） */}
      <button
        type="button"
        title="在场芯片：离开=inform 镜像推企微（默认关），回来时自动合成隔夜账卡"
        onClick={() => setAway(!presence.away)}
        className="cursor-pointer whitespace-nowrap hover:text-foreground"
      >
        {presence.away ? "离开" : "在座"}
      </button>
      {gap}
      <span className="whitespace-nowrap text-faint">
        {port ? `:${port}` : "本机"}
      </span>
    </div>
  );
}
