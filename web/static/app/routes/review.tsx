/** 回看模式（theater.md §9）：舞台叙事章节——「这周它都干了什么」
 * 用章节回答，而不是流水账。数据同源纪律（§15.5 裁决）：任务章
 * 节来自任务板（tasks API），叙事章节来自既有分集管道（recap），
 * 读数来自用量台账——不造第二套账本。分工钉死：本页管章节叙事，
 * 引擎室（轨迹页）管步级账本，每章「查看轨迹」深链定位到步。 */
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";

import { QueryErrorBanner } from "~/components/query-error-banner";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { fetchRecap, fetchTasks, fetchUsage } from "~/lib/api";
import { cn } from "~/lib/utils";
import type { AgentTask } from "~/lib/types";

const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

function fmtDuration(ms: number): string {
  const m = Math.max(1, Math.round(ms / 60_000));
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

function oneLine(s: string, n = 60): string {
  const line = s.split("\n").find((l) => l.trim());
  return (line ?? "").trim().slice(0, n) || "(空)";
}

export function meta() {
  return [{ title: "mindloop · 回看" }];
}

export default function Review() {
  const { identityId = "" } = useParams();
  const base = `/i/${encodeURIComponent(identityId)}`;

  const tasks = useQuery({
    queryKey: ["review-tasks", identityId],
    queryFn: () => fetchTasks(identityId),
    refetchInterval: 30000,
  });
  const recap = useQuery({
    queryKey: ["recap", identityId],
    queryFn: () => fetchRecap(identityId),
    staleTime: 60_000,
  });
  const usage = useQuery({
    queryKey: ["review-usage", identityId],
    queryFn: () => fetchUsage(identityId),
    staleTime: 60_000,
  });

  // 「现在」的纯来源：任务数据的抓取时刻（≤30s 陈旧，对 7 天窗口
  // 足够）——渲染期不调 Date.now（React 纯函数纪律）。
  const now = tasks.dataUpdatedAt || 0;
  const closed = (tasks.data ?? []).filter(
    (t) =>
      (t.status === "succeeded" || t.status === "failed") &&
      Date.parse(t.updated_at) > now - WEEK_MS
  );
  const sorted = [...closed].sort(
    (a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)
  );
  const succeeded = closed.filter((t) => t.status === "succeeded").length;
  const episodes = (recap.data?.episodes ?? []).slice(0, 8);
  const weekCalls =
    usage.data?.daily
      ?.filter(([day]) => Date.parse(`${day}T00:00:00Z`) > now - WEEK_MS)
      .reduce((acc, [, d]) => acc + d.calls, 0) ?? null;

  if (tasks.isLoading) {
    return (
      <div className="space-y-3 py-2">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-20 w-full rounded-xl" />
        <Skeleton className="h-20 w-full rounded-xl" />
      </div>
    );
  }
  if (tasks.isError) {
    return (
      <QueryErrorBanner
        error={tasks.error}
        onRetry={() => void tasks.refetch()}
      />
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div className="mb-4 flex items-baseline gap-3">
        <h1 className="font-note text-[19px] font-semibold">回看</h1>
        <span className="font-mono text-[11px] text-muted-foreground">
          近 7 天 · 舞台管叙事章节，引擎室管步级账本 · 数据与任务板/分集/用量同源
        </span>
      </div>

      <div className="mb-5 rounded-xl border border-border bg-card p-4">
        <div className="mb-2 text-[12px] font-semibold text-foreground">本周读数</div>
        <div className="flex flex-wrap gap-x-5 gap-y-1 font-mono text-[11px] text-muted-foreground">
          <span>
            任务收口 <b className="text-foreground">{closed.length}</b>
            {closed.length > 0 && `（成功 ${succeeded} · 失败 ${closed.length - succeeded}）`}
          </span>
          <span>
            叙事章节 <b className="text-foreground">{episodes.length}</b>
          </span>
          {weekCalls != null && (
            <span>
              近 7 天调用 <b className="text-foreground">{weekCalls}</b> 次
            </span>
          )}
          {usage.data?.totals && (
            <span>
              累计 token{" "}
              <b className="text-foreground">
                {(
                  (usage.data.totals.in +
                    usage.data.totals.out +
                    usage.data.totals.think) /
                  1000
                ).toFixed(0)}
                K
              </b>
            </span>
          )}
        </div>
      </div>

      {sorted.length === 0 && episodes.length === 0 && (
        <div className="rounded-xl border border-border bg-card p-6 text-center text-sm text-muted-foreground">
          近 7 天没有收口的任务，也没有可讲的章节。
        </div>
      )}

      {sorted.length > 0 && (
        <>
          <div className="mb-2 font-mono text-[10px] tracking-[0.1em] text-muted-foreground">
            任务章节 · 近 7 天收口
          </div>
          <div className="mb-5 space-y-2.5">
            {sorted.map((t) => (
              <TaskChapter key={t.task_id} task={t} base={base} />
            ))}
          </div>
        </>
      )}

      {episodes.length > 0 && (
        <>
          <div className="mb-2 font-mono text-[10px] tracking-[0.1em] text-muted-foreground">
            叙事章节 · 分集（更早的完整章节见分集页）
          </div>
          <div className="space-y-2.5">
            {episodes.map((ep) => (
              <div
                key={ep.idx}
                data-testid="review-chapter"
                className="rounded-xl border border-border bg-card p-4"
              >
                <div className="mb-1.5 flex items-baseline gap-2.5">
                  <span className="text-[13px] font-semibold text-foreground">
                    {ep.title || `情节 ${ep.idx}`}
                  </span>
                  <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
                    {ep.first_ts} → {ep.last_ts} · {ep.n_steps} 步
                  </span>
                </div>
                {ep.summary && (
                  <p className="text-[12px] leading-relaxed text-muted-foreground">
                    {ep.summary}
                  </p>
                )}
                <Link
                  to={`${base}?step=${encodeURIComponent(ep.first_step)}`}
                  className="mt-1.5 inline-block text-[11.5px] text-primary hover:underline"
                >
                  查看轨迹 →
                </Link>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

function TaskChapter({ task, base }: { task: AgentTask; base: string }) {
  const ok = task.status === "succeeded";
  const durationMs =
    Date.parse(task.updated_at) - Date.parse(task.created_at) || 0;
  const lastStep = task.events[task.events.length - 1]?.step_id;
  return (
    <div
      data-testid="review-chapter"
      className="rounded-xl border border-border bg-card p-4"
    >
      <div className="mb-1.5 flex items-baseline gap-2.5">
        <span
          className={cn(
            "rounded-full px-2 py-0.5 font-mono text-[10px]",
            ok ? "bg-primary/10 text-primary" : "bg-destructive/10 text-destructive"
          )}
        >
          {ok ? "完成" : "失败"}
        </span>
        <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-foreground">
          {oneLine(task.content)}
        </span>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {durationMs > 0 ? `${fmtDuration(durationMs)} · ` : ""}
          {task.events.length} 步 · 你参与 {task.attempt} 次内
        </span>
      </div>
      {(task.result || (task.deliverables?.length ?? 0) > 0) && (
        <p className="text-[12px] leading-relaxed text-muted-foreground">
          {task.deliverables?.length
            ? `产物 ${task.deliverables.join(" · ")}`
            : oneLine(task.result ?? "", 100)}
        </p>
      )}
      {lastStep && (
        <Link
          to={`${base}?step=${encodeURIComponent(lastStep)}`}
          className="mt-1.5 inline-block text-[11.5px] text-primary hover:underline"
        >
          查看轨迹 →
        </Link>
      )}
    </div>
  );
}
