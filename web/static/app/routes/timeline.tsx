import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useLocation, useNavigate, useParams } from "react-router";

import { QueryErrorBanner } from "~/components/query-error-banner";
import { TimelineView } from "~/components/timeline-view";
import { VitalStrip } from "~/components/vital-strip";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { fetchIdentityStatus } from "~/lib/api";
import { STATUS_ACTIVE_POLL_MS } from "~/lib/polling";
import { useMindlog } from "~/lib/use-mindlog";
import { stepColor } from "~/lib/step-colors";
import { buildTimeline } from "~/lib/timeline-model";
import { TrajContext } from "~/lib/traj-context";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 轨迹" }];
}

// 因果边图例（分类学取色，与 timeline-palette.EDGE_STROKE 同源）：
// 触发=树脂 · 唤醒=苔 · 写入=湖 · 合并=梅。色值走 CSS 变量，SVG 以
// style 注入（presentation attribute 不解析 var()）。
const EDGE_LEGEND = [
  { label: "触发 → 运行", color: "var(--resin)", opacity: 0.9 },
  { label: "唤醒 → 思考者", color: "var(--moss)", opacity: 0.3 },
  { label: "运行 → 写入", color: "var(--lake)", opacity: 0.3 },
  { label: "分叉 → 合并", color: "var(--plum)", opacity: 0.3 },
];

function TimelineSkeleton() {
  return (
    <div className="space-y-3">
      <Skeleton className="h-[34px] rounded-md" />
      <div className="rounded-lg border border-line p-3">
        <div className="mb-3 flex gap-3">
          <Skeleton className="h-5 w-20" />
          <Skeleton className="h-5 w-20" />
          <Skeleton className="h-5 w-20" />
        </div>
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div key={i} className="flex items-center gap-2 py-1.5">
            <Skeleton className="h-3 w-14" />
            <Skeleton className="h-3 w-3 rounded-sm" />
            <Skeleton
              className="h-3"
              style={{ width: `${30 + ((i * 13) % 40)}%` }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

export default function TimelinePage() {
  const { identityId = "" } = useParams();
  const navigate = useNavigate();
  const { pathname, search } = useLocation();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_ACTIVE_POLL_MS,
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

  const layout = useMemo(
    () => (mindlog ? buildTimeline(mindlog) : null),
    [mindlog]
  );

  const typesPresent = useMemo(() => {
    const seen = new Set<string>();
    for (const cell of layout?.cells ?? []) seen.add(cell.step.type);
    return [...seen];
  }, [layout]);

  // 生命线点击 → 该时刻最近一步的泳道深链（?step= 由 TimelineView 解析：
  // 窗口内滚动+闪烁+开详情，窗口外走取单步弹窗）。
  const jumpToStep = useMemo(() => {
    if (!mindlog) return undefined;
    return (step: { step_id: string | null }) => {
      if (!step.step_id) return;
      const params = new URLSearchParams(search);
      params.set("step", step.step_id);
      navigate({ pathname, search: params.toString() });
    };
  }, [mindlog, navigate, pathname, search]);

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <TimelineSkeleton />
      </div>
    );
  }

  if (!mindlog || !layout) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无轨迹</EmptyTitle>
            <EmptyDescription>
              尚未产生步骤——启动该身份后，轨迹会出现在这里。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    );
  }

  return (
    <TrajContext.Provider value={{ identityId, trajId: mindlog.traj_id }}>
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <VitalStrip
          steps={mindlog.steps}
          live={live}
          onJump={jumpToStep}
          className="mb-3"
        />
        <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <span className="font-mono text-xs text-muted-foreground">
            {hiddenOlder > 0
              ? `最近 ${mindlog.steps.length} / 共 ${mindlog.step_count} 步`
              : `${mindlog.step_count} 步`}{" "}
            · {mindlog.runs.length} 次运行
          </span>
          {hiddenOlder > 0 && (
            <button
              type="button"
              className="text-xs text-muted-foreground underline hover:text-foreground disabled:opacity-50"
              disabled={loadingOlder}
              onClick={() => void loadOlder()}
            >
              {loadingOlder ? "加载中…" : "加载更早"}
            </button>
          )}
          <div className="ml-auto flex flex-wrap items-center gap-x-3 gap-y-1">
            {typesPresent.map((type) => (
              <span
                key={type}
                className={cn(
                  "rounded px-1.5 py-0.5 font-mono text-[10px]",
                  stepColor(type).chip
                )}
              >
                {type}
              </span>
            ))}
            <span className="mx-1 h-4 w-px bg-border" />
            {EDGE_LEGEND.map(({ label, color, opacity }) => (
              <span
                key={label}
                className="flex items-center gap-1 font-mono text-[10px] text-muted-foreground"
              >
                <svg width="18" height="8">
                  <line
                    x1="1"
                    y1="4"
                    x2="14"
                    y2="4"
                    strokeWidth="1.5"
                    opacity={opacity}
                    style={{ stroke: color }}
                  />
                  <circle
                    cx="15.5"
                    cy="4"
                    r="2.5"
                    opacity={opacity}
                    style={{ fill: color }}
                  />
                </svg>
                {label}
              </span>
            ))}
          </div>
        </div>
        <TimelineView layout={layout} live={live} />
      </div>
    </TrajContext.Provider>
  );
}
