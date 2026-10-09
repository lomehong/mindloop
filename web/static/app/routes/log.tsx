// 日志流（docs/designs/ui-language.md §7 B）：轨迹三视图之一。旧「思维
// 日志」密集流与「思维日志 v2」卡片流在此合并为唯一日志视图——左侧
// 时间戳轨（mono）+ 步骤流（心智的话用衬线）+ 类型/来源过滤；粉青黑
// 皮全弃，随年轮 token。

import { useQuery } from "@tanstack/react-query";
import { FoldVertical, UnfoldVertical } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router";

import { FollowPin } from "~/components/follow-pin";
import { ForkTree } from "~/components/fork-tree";
import { StepModal } from "~/components/mindlog-search";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { assembleStream, StreamItems } from "~/components/stream";
import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { fetchIdentityStatus } from "~/lib/api";
import { dayLabel, formatClock, localDay } from "~/lib/format";
import { TrajContext } from "~/lib/traj-context";
import { useMindlog } from "~/lib/use-mindlog";
import type { NormalizedStep, RunGroup } from "~/lib/types";

export function meta() {
  return [{ title: "mindloop · 日志流" }];
}

/** 把一步滚到视野中央并闪烁——跳转（搜索/分集/生命线）后需要在一屏
 * 上百行里一眼找到它。（子轨迹页也用它做步内跳转。） */
export function scrollToStep(step: {
  step_id: string | null;
  run_id?: string | null;
}) {
  const el =
    document.getElementById(`step-${step.step_id}`) ??
    (step.run_id ? document.getElementById(`step-${step.run_id}`) : null);
  el?.scrollIntoView({ behavior: "smooth", block: "center" });
  if (el) {
    el.classList.add("tl-flash");
    setTimeout(() => el.classList.remove("tl-flash"), 3000);
  }
}

function LogSkeleton() {
  return (
    <div className="flex gap-4">
      <Skeleton className="hidden h-64 w-52 md:block" />
      <div className="min-w-0 flex-1 space-y-2 rounded-xl border border-line p-3">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="h-3 w-12" />
            <Skeleton className="h-3 w-3 rounded-sm" />
            <Skeleton
              className="h-3"
              style={{ width: `${35 + ((i * 17) % 45)}%` }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

/** 运行跳转项标签：MM-DD HH:MM · 思考者 · N 步。 */
function runOptionLabel(r: RunGroup): string {
  const day = localDay(r.started_ts);
  const when = day
    ? `${dayLabel(day)} ${formatClock(r.started_ts).slice(0, 5)}`
    : r.started_ts.slice(0, 16);
  return `${when} · ${r.launched_by ?? "—"} · ${r.step_ids.length}步`;
}

export default function LogPage() {
  const { identityId = "" } = useParams();
  const [hideParam, setHideParam] = useQueryState(
    "hide",
    parseAsString.withDefault("")
  );
  const [sourceFilter, setSourceFilter] = useQueryState(
    "source",
    parseAsString.withDefault("all")
  );
  // 深链（?step=）：分集与搜索命中直接定位到日志流中的一步。
  const [stepParam, setStepParam] = useQueryState("step", parseAsString.withDefault(""));
  const [expandAll, setExpandAll] = useState(false);

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 2000,
  });
  const live = status?.live ?? false;

  const {
    data: mindlog,
    isLoading,
    isError,
    error,
    refetch,
    loadOlder,
    loadingOlder,
    hiddenOlder,
  } = useMindlog(identityId, live);

  const hidden = useMemo(
    () => new Set(hideParam.split(",").filter(Boolean)),
    [hideParam]
  );

  const stream = useMemo(
    () => (mindlog ? assembleStream(mindlog.steps, mindlog.runs) : []),
    [mindlog]
  );

  useEffect(() => {
    if (!stepParam || !mindlog) return;
    const target = mindlog.steps.find((step) =>
      step.step_id?.startsWith(stepParam)
    );
    if (!target) return;
    // The step may render as its own card, inside a run group, or — for
    // action steps that triggered a run — as the run group's header.
    const triggeredRun = mindlog.runs.find(
      (run) => run.trigger_step_id === target.step_id
    );
    const candidates = [target.step_id, target.run_id, triggeredRun?.run_id]
      .filter(Boolean)
      .map((id) => `step-${id}`);
    // A large mind log takes a while to render — retry until an element
    // exists, then scroll + highlight.
    let tries = 0;
    let timer: ReturnType<typeof setTimeout>;
    const attempt = () => {
      const el = candidates
        .map((id) => document.getElementById(id))
        .find(Boolean);
      if (el) {
        // Instant, not smooth: late layout shifts on a large log cancel the
        // smooth animation. Re-assert once after things settle.
        el.scrollIntoView({ block: "center" });
        el.classList.add("bg-primary/10");
        setTimeout(() => el.scrollIntoView({ block: "center" }), 600);
        setTimeout(() => el.classList.remove("bg-primary/10"), 2000);
        return;
      }
      if (tries++ < 40) timer = setTimeout(attempt, 250);
    };
    timer = setTimeout(attempt, 100);
    return () => clearTimeout(timer);
    // scroll once per navigation, not on live-poll refreshes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stepParam, mindlog?.traj_id]);

  // 深链（?run=）：任务卡「运行记录」定位到该次运行的分组节点
  // （运行组的锚点 id 即 step-<run_id>，见 scrollToStep 的回退分支）；
  // 大日志渲染慢，重试到元素出现；即时定位 + 600ms 再校准一次——
  // 盖过 FollowPin 首次加载的跳尾与迟到布局。找不到（窗口外的旧运行）
  // 静默放弃。
  const [runParam, setRunParam] = useQueryState("run", parseAsString.withDefault(""));
  useEffect(() => {
    if (!runParam || !mindlog) return;
    let tries = 0;
    let timer: ReturnType<typeof setTimeout>;
    const attempt = () => {
      const el = document.getElementById(`step-${runParam}`);
      if (el) {
        el.scrollIntoView({ block: "center" });
        el.classList.add("tl-flash");
        setTimeout(() => el.scrollIntoView({ block: "center" }), 600);
        setTimeout(() => el.classList.remove("tl-flash"), 3000);
        return;
      }
      if (tries++ < 40) timer = setTimeout(attempt, 250);
    };
    timer = setTimeout(attempt, 100);
    return () => clearTimeout(timer);
    // scroll once per navigation, not on live-poll refreshes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runParam, mindlog?.traj_id]);

  // Deeplink to a step older than the loaded window (e.g. a search jump
  // from another tab): show it in a modal instead of scrolling.
  const [modalStep, setModalStep] = useState<string | null>(null);
  useEffect(() => {
    if (!stepParam || !mindlog) return;
    const inWindow = mindlog.steps.some((step) =>
      step.step_id?.startsWith(stepParam)
    );
    if (!inWindow) setModalStep(stepParam);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stepParam, mindlog?.traj_id]);

  const typeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const step of mindlog?.steps ?? []) {
      counts.set(step.type, (counts.get(step.type) ?? 0) + 1);
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1]);
  }, [mindlog]);

  const sources = useMemo(() => {
    const set = new Set<string>();
    for (const step of mindlog?.steps ?? []) {
      if (step.source) set.add(step.source);
    }
    return [...set].sort();
  }, [mindlog]);

  const visible = useMemo(() => {
    const sourceOk = (step: NormalizedStep) =>
      sourceFilter === "all" || step.source === sourceFilter;
    return stream.filter((item) => {
      if (item.kind === "run") return !hidden.has("shellm-run");
      if (item.kind === "idle")
        return (
          !hidden.has("idle") &&
          (sourceFilter === "all" || item.steps.some(sourceOk))
        );
      return !hidden.has(item.step.type) && sourceOk(item.step);
    });
  }, [stream, hidden, sourceFilter]);

  // 跳转器（长日志定位，2026-10-06）：日期 → 该日首步；运行 → 运行组
  // 锚点。两者都走既有 ?step= / ?run= 深链（滚动+闪烁；窗口外步骤自动
  // 退化为取单步弹窗）。
  const jumpDays = useMemo(() => {
    const seen = new Set<string>();
    const days: string[] = [];
    for (const s of mindlog?.steps ?? []) {
      const d = localDay(s.ts);
      if (d && !seen.has(d)) {
        seen.add(d);
        days.push(d);
      }
    }
    return days.sort().reverse();
  }, [mindlog]);
  const jumpRuns = useMemo(
    () =>
      [...(mindlog?.runs ?? [])].sort((a, b) =>
        (b.started_ts ?? "").localeCompare(a.started_ts ?? "")
      ),
    [mindlog]
  );
  const jumpToDay = (day: string) => {
    const first = (mindlog?.steps ?? []).find(
      (s) => s.step_id && localDay(s.ts) === day
    );
    if (first?.step_id) setStepParam(first.step_id);
  };

  const toggleType = (type: string) => {
    const next = new Set(hidden);
    if (next.has(type)) next.delete(type);
    else next.add(type);
    setHideParam([...next].join(",") || null);
  };

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <LogSkeleton />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (!mindlog) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无轨迹</EmptyTitle>
            <EmptyDescription>尚未产生步骤——启动该身份后，日志会出现在这里。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    );
  }

  return (
    <TrajContext.Provider value={{ identityId, trajId: mindlog.traj_id }}>
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mb-3 flex flex-wrap items-center gap-3">
          <span className="font-mono text-xs text-muted-foreground">
            {hiddenOlder > 0
              ? `最近 ${mindlog.steps.length} / 共 ${mindlog.step_count} 步`
              : `${mindlog.step_count} 步`}{" "}
            · {mindlog.runs.length} 次运行
            {live && " · 实时"}
          </span>
          <div className="ml-auto">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setExpandAll((v) => !v)}
              className="gap-1.5 text-xs"
            >
              {expandAll ? (
                <>
                  <FoldVertical className="h-3.5 w-3.5" /> 收起全部
                </>
              ) : (
                <>
                  <UnfoldVertical className="h-3.5 w-3.5" /> 展开全部
                </>
              )}
            </Button>
          </div>
        </div>

        <div className="flex gap-4">
          <aside className="hidden w-52 shrink-0 md:block">
            <div className="sticky top-28 max-h-[calc(100vh-8rem)] space-y-5 overflow-y-auto pb-4">
              {/* 长日志跳转器：放吸附左栏（翻到哪都在）——放在页头会随
                  滚动划走，等于没有（2026-10-08 反馈）。 */}
              <div>
                <h3 className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-faint">
                  跳转
                </h3>
                <div className="space-y-1.5">
                  <Select onValueChange={(v) => jumpToDay(v)}>
                    <SelectTrigger className="h-8 w-full font-mono text-xs">
                      <SelectValue placeholder="跳到日期…" />
                    </SelectTrigger>
                    <SelectContent>
                      {jumpDays.map((d) => (
                        <SelectItem key={d} value={d} className="font-mono text-xs">
                          {dayLabel(d)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Select onValueChange={(v) => setRunParam(v)}>
                    <SelectTrigger className="h-8 w-full font-mono text-xs">
                      <SelectValue placeholder="跳到运行…" />
                    </SelectTrigger>
                    <SelectContent>
                      {jumpRuns.map((r) => (
                        <SelectItem
                          key={r.run_id}
                          value={r.run_id}
                          className="font-mono text-xs"
                        >
                          {runOptionLabel(r)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div>
                <h3 className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-faint">
                  步骤类型
                </h3>
                <div className="space-y-1">
                  {typeCounts.map(([type, count]) => (
                    <label
                      key={type}
                      className="flex cursor-pointer items-center gap-2 font-mono text-xs"
                    >
                      <Checkbox
                        checked={!hidden.has(type)}
                        onCheckedChange={() => toggleType(type)}
                      />
                      <span className="min-w-0 flex-1 truncate">{type}</span>
                      <span className="tabular-nums text-muted-foreground">
                        {count}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
              {sources.length > 0 && (
                <div>
                  <h3 className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-faint">
                    来源
                  </h3>
                  <Select
                    value={sourceFilter}
                    onValueChange={(v) => setSourceFilter(v === "all" ? null : v)}
                  >
                    <SelectTrigger className="h-8 w-full font-mono text-xs">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">全部来源</SelectItem>
                      {sources.map((source) => (
                        <SelectItem
                          key={source}
                          value={source}
                          className="font-mono text-xs"
                        >
                          {source}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}
              <ForkTree
                identityId={identityId}
                currentTrajId={mindlog.traj_id}
                live={live}
              />
            </div>
          </aside>

          <div className="min-w-0 flex-1 rounded-xl border border-line bg-card px-2 py-2">
            {hiddenOlder > 0 && (
              <div className="flex justify-center py-1.5">
                <Button
                  variant="outline"
                  size="sm"
                  className="text-xs"
                  disabled={loadingOlder}
                  onClick={() => void loadOlder()}
                >
                  {loadingOlder
                    ? "加载中…"
                    : `加载更早（还有 ${hiddenOlder} 步）`}
                </Button>
              </div>
            )}
            {visible.length === 0 ? (
              <div className="px-2 py-10 text-center text-sm text-muted-foreground">
                还没有步骤——启动该身份后，活动会写进这里。
              </div>
            ) : (
              <StreamItems items={visible} expandAll={expandAll} live={live} rail />
            )}
          </div>
        </div>
        <FollowPin live={live} stepCount={mindlog.step_count} />
        {modalStep && (
          <StepModal
            identityId={identityId}
            stepId={modalStep}
            stepCount={mindlog.step_count}
            onClose={() => setModalStep(null)}
          />
        )}
      </div>
    </TrajContext.Provider>
  );
}
