import { useQuery } from "@tanstack/react-query";
import { Link, useLocation } from "react-router";

import { PetDock } from "~/components/pet/pet-dock";
import {
  fetchActivity,
  fetchIdentities,
  fetchThinkers,
  fetchUsage,
} from "~/lib/api";
import { STATUS_BACKGROUND_POLL_MS } from "~/lib/polling";

// Trellis 右·身份档案（docs/designs/ui-language.md §2.3）：圆角卡片
// 挂在身份页右侧，把"查状态"类信息（活性/预算/思考者）从页间跳转
// 收进一张常驻卡。数据全部来自既有 API，静默失败退化为"无读数"。
// 仅在 /i/:id 路由渲染（root.tsx 控制挂载），窄屏隐藏。

function ageText(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return "—";
  if (seconds < 60) return `${Math.round(seconds)} 秒前`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} 小时前`;
  return `${Math.round(seconds / 86400)} 天前`;
}

function silent<T>(fn: () => Promise<T>): () => Promise<T | null> {
  return async () => {
    try {
      return await fn();
    } catch {
      return null;
    }
  };
}

const THINKER_DOT: Record<string, string> = {
  running: "bg-primary",
  active: "bg-primary",
  draining: "bg-resin",
  idle: "bg-muted-foreground/40",
  stopped: "bg-muted-foreground/30",
  disabled: "bg-muted-foreground/20",
};

export function IdentityDossier() {
  const { pathname } = useLocation();
  const identityId = decodeURIComponent(pathname.split("/")[2] ?? "");

  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30000,
    staleTime: 10_000,
  });
  const me = identities?.find((it) => it.id === identityId) ?? null;

  const { data: activity } = useQuery({
    queryKey: ["dossier-activity", identityId],
    queryFn: silent(() => fetchActivity(identityId)),
    enabled: identityId !== "",
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: status } = useQuery({
    queryKey: ["dossier-status", identityId],
    queryFn: silent(() => import("~/lib/api").then(({ fetchIdentityStatus }) => fetchIdentityStatus(identityId))),
    enabled: identityId !== "",
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: thinkers } = useQuery({
    queryKey: ["dossier-thinkers", identityId],
    queryFn: silent(() => fetchThinkers(identityId)),
    enabled: identityId !== "",
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: usage } = useQuery({
    queryKey: ["dossier-usage", identityId],
    queryFn: silent(() => fetchUsage(identityId)),
    enabled: identityId !== "",
    refetchInterval: 300_000,
  });

  const admission = usage?.admission ?? null;
  const budgetRatio =
    admission && admission.daily_limit > 0
      ? Math.min(1, admission.used_today / admission.daily_limit)
      : null;

  return (
    <aside className="hidden w-[276px] shrink-0 flex-col gap-3 overflow-y-auto rounded-2xl border border-border bg-card p-3 xl:flex">
      {/* 摘要卡：停靠的生命体——静态缩略升级为活宠物（点击出快捷菜单），
          名字与运行态行沿用档案自己的读数 */}
      <div className="rounded-xl bg-muted p-3">
        <PetDock fixedIdentityId={identityId}>
          <div className="min-w-0">
            <div className="truncate text-[13.5px] font-semibold">
              {me?.name ?? identityId}
            </div>
            <div className="text-[11px] text-muted-foreground">
              {me ? (me.live ? "心智运行中" : "已停止") : "—"}
              {" · "}
              {ageText(activity?.last_step_age_s ?? null)}
            </div>
          </div>
        </PetDock>
      </div>

      {/* 环境信息 */}
      <section>
        <div className="px-1 pb-1 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          环境信息
        </div>
        <div className="rounded-xl border border-border px-3 py-2 text-[12.5px]">
          <div className="flex justify-between py-0.5">
            <span className="text-muted-foreground">步骤</span>
            <span className="font-mono">{status?.step_count?.toLocaleString() ?? "—"}</span>
          </div>
          <div className="flex justify-between py-0.5">
            <span className="text-muted-foreground">活性</span>
            <span className="font-mono">
              {activity ? ACTIVITY_LABEL[activity.state] ?? activity.state : "—"}
            </span>
          </div>
          <div className="flex justify-between py-0.5">
            <span className="text-muted-foreground">调度器</span>
            <span className="font-mono">
              {activity ? (activity.dispatcher_running ? "运行中" : "已停止") : "—"}
            </span>
          </div>
        </div>
      </section>

      {/* 预算 */}
      <section>
        <div className="flex items-center justify-between px-1 pb-1 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          <span>今日预算</span>
          {admission?.cooling_until && <span className="text-destructive">熔断冷却中</span>}
        </div>
        <div className="rounded-xl border border-border px-3 py-2.5">
          {budgetRatio !== null && admission ? (
            <>
              <div className="mb-1.5 flex justify-between text-[12px]">
                <span className="font-mono">{Math.round(budgetRatio * 100)}%</span>
                <span className="font-mono text-muted-foreground">
                  {admission.used_today.toLocaleString()} / {admission.daily_limit.toLocaleString()}
                </span>
              </div>
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                  className={`h-full rounded-full ${budgetRatio >= 0.8 ? "bg-resin" : "bg-primary"}`}
                  style={{ width: `${Math.round(budgetRatio * 100)}%` }}
                />
              </div>
            </>
          ) : (
            <div className="text-[12px] text-muted-foreground">未设预算</div>
          )}
        </div>
      </section>

      {/* 思考者 */}
      <section>
        <div className="px-1 pb-1 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          思考者
        </div>
        <div className="rounded-xl border border-border px-3 py-1.5 text-[12.5px]">
          {(thinkers?.thinkers?.length ?? 0) === 0 && (
            <div className="py-1 text-muted-foreground">暂无</div>
          )}
          {thinkers?.thinkers?.map((t) => (
            <div key={t.name} className="flex items-center gap-2 py-1">
              <span className={`h-1.5 w-1.5 rounded-full ${THINKER_DOT[t.state] ?? "bg-muted-foreground/30"}`} />
              <span className="truncate font-mono text-[12px]">{t.name}</span>
              <span className="ml-auto text-[11px] text-muted-foreground">{t.state}</span>
            </div>
          ))}
        </div>
      </section>

      {/* 深入链接 */}
      <div className="mt-auto flex gap-2 px-1 pt-1 text-[12px]">
        <Link className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline" to={`/i/${encodeURIComponent(identityId)}/run/health`}>
          健康
        </Link>
        <Link className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline" to={`/i/${encodeURIComponent(identityId)}/run/usage`}>
          用量
        </Link>
        <Link className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline" to={`/i/${encodeURIComponent(identityId)}/run/thinkers`}>
          思考者管理
        </Link>
      </div>
    </aside>
  );
}

const ACTIVITY_LABEL: Record<string, string> = {
  working: "工作中",
  stalled: "停滞",
  idle: "空闲",
  asleep: "休眠",
};
