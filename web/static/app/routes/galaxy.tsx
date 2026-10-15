// 星系（avatar.md §6.3/D 期：多心智远景）——时间望远镜拉远到底的
// 形态。一星一心智：亮度=运行态、告警环=需要你（待批/微饿）、悬停
// tip=真实读数、点星下钻进它的舞台。首屏即同行面的远景形态，数据与
// 工作台同一批既有端点（后端零改动）；DOM 星而非 three.js——远景的
// 信息密度不需要着色器，宁省帧预算给舞台宇宙。

import { useQueries, useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { fetchActivity, fetchApprovals, fetchIdentities, fetchUsage } from "~/lib/api";
import { phaseFor } from "~/lib/pet-state";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 星系" }];
}

/** 确定性星位（同一身份恒同一片天区），散布在中央 12%~88% 区域。 */
function starPos(id: string): { left: number; top: number } {
  let h = 2166136261;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  const a = ((h >>> 0) % 1000) / 1000;
  const b = ((h >>> 10) % 1000) / 1000;
  return { left: 12 + a * 76, top: 18 + b * 64 };
}

export default function Galaxy() {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const list = identities ?? [];
  const usageQ = useQueries({
    queries: list.map((it) => ({
      queryKey: ["usage", it.id],
      queryFn: () => fetchUsage(it.id),
      refetchInterval: USAGE_IDLE_POLL_MS,
    })),
  });
  const approvalsQ = useQueries({
    queries: list.map((it) => ({
      queryKey: ["approvals", it.id],
      queryFn: () => fetchApprovals(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const activityQ = useQueries({
    queries: list.map((it) => ({
      queryKey: ["activity", it.id],
      queryFn: () => fetchActivity(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });

  return (
    <div className="relative h-full overflow-hidden rounded-xl border border-line bg-[#0d1210]">
      {/* 星空底：确定性散点（纯 CSS，无动画开销；reduced-motion 友好）。 */}
      <div
        aria-hidden
        className="absolute inset-0 opacity-70"
        style={{
          background:
            "radial-gradient(1px 1px at 12% 22%, rgba(230,236,225,.5) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 78% 14%, rgba(230,236,225,.4) 50%, transparent 51%)," +
            "radial-gradient(1.5px 1.5px at 62% 74%, rgba(230,236,225,.35) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 30% 68%, rgba(230,236,225,.4) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 88% 55%, rgba(230,236,225,.3) 50%, transparent 51%)," +
            "radial-gradient(1.5px 1.5px at 44% 32%, rgba(183,155,209,.4) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 8% 82%, rgba(230,236,225,.3) 50%, transparent 51%)",
        }}
      />
      <div className="absolute left-1/2 top-5 z-10 flex -translate-x-1/2 items-baseline gap-2">
        <span className="text-[14px] font-medium text-foreground/90">星系</span>
        <span className="font-mono text-[10.5px] text-faint">
          {list.length} 具心智 · 点星进它的舞台 · 告警环 = 需要你
        </span>
        <Link
          to="/"
          className="ml-3 rounded-lg border border-border/60 px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground hover:text-foreground"
        >
          ← 同行面
        </Link>
      </div>

      {list.length === 0 && (
        <div className="absolute inset-0 flex items-center justify-center">
          <p className="text-[13px] text-faint">
            留白的夜空——去同行面创建第一具心智，这里会亮起第一颗引路星。
          </p>
        </div>
      )}

      {list.map((it, i) => {
        const pos = starPos(it.id);
        const admission = usageQ[i]?.data?.admission;
        const pct =
          admission && admission.daily_limit > 0
            ? Math.round((admission.used_today / admission.daily_limit) * 100)
            : null;
        const hungry = pct !== null && pct >= 80;
        const waiting = approvalsQ[i]?.data?.length ?? 0;
        const busy = activityQ[i]?.data?.busy_thinkers.length ?? 0;
        const needYou = waiting > 0 || hungry;
        return (
          <Link
            key={it.id}
            to={`/i/${encodeURIComponent(it.id)}`}
            data-testid={`star-${it.id}`}
            className="group absolute z-10"
            style={{ left: `${pos.left}%`, top: `${pos.top}%` }}
          >
            <span
              className={cn(
                "block rounded-full transition-transform group-hover:scale-125",
                it.live ? "size-7" : "size-4 opacity-50"
              )}
              style={{
                background: it.live
                  ? `radial-gradient(circle at 35% 30%, var(--color-moss, #8fc166), #22301f 78%)`
                  : `radial-gradient(circle at 35% 30%, #3a4540, #161b18 78%)`,
                boxShadow: it.live
                  ? "0 0 22px color-mix(in srgb, var(--color-moss, #8fc166) 40%, transparent)"
                  : "none",
                animation: `galaxy-drift ${8 + phaseFor(it.id) * 7}s ease-in-out infinite alternate`,
              }}
            />
            {needYou && (
              <span
                aria-hidden
                className="absolute -inset-2 rounded-full border border-resin/70"
                style={{ animation: "pulse 2.2s infinite" }}
              />
            )}
            <span className="pointer-events-none absolute left-8 top-0 z-20 hidden w-max max-w-56 rounded-lg border border-border bg-card px-3 py-2 text-left text-[12px] text-muted-foreground shadow-lg group-hover:block">
              <b className="text-foreground">{it.name}</b>
              <br />
              {it.live ? (busy > 0 ? `工作中 · ${busy} 思考者忙碌` : "运行中 · 空闲") : "未运行"}
              {pct !== null && <> · 食量 {pct}%{hungry ? "（微饿）" : ""}</>}
              {waiting > 0 && <> · {waiting} 件等你拍板</>}
            </span>
          </Link>
        );
      })}

      <div className="absolute bottom-3 left-0 right-0 text-center font-mono text-[11px] text-faint">
        星系是远景——近景是舞台，中景是日记 · 亮度=运行态 · 琥珀环=需要你（待批/微饿）
      </div>
    </div>
  );
}
