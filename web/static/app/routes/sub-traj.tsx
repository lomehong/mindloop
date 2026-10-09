import { useQuery } from "@tanstack/react-query";
import { FoldVertical, UnfoldVertical } from "lucide-react";
import { Fragment, useMemo, useState } from "react";
import { Link, useParams } from "react-router";

import { ForkTree } from "~/components/fork-tree";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { assembleStream, StreamItems } from "~/components/stream";
import { TimelineBar } from "~/components/timeline-bar";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  fetchIdentityStatus,
  fetchSubTraj,
  HttpError,
  pollWhileLive,
} from "~/lib/api";
import { LiveBadge } from "~/components/live-badge";
import { TrajContext } from "~/lib/traj-context";
import { scrollToStep } from "~/routes/log";

export function meta() {
  return [{ title: "mindloop · 子轨迹" }];
}

function SubTrajSkeleton() {
  return (
    <div className="flex gap-4">
      <Skeleton className="hidden h-64 w-52 md:block" />
      <div className="min-w-0 flex-1 space-y-2 rounded-xl border border-line p-3">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="h-3 w-12" />
            <Skeleton className="h-3 w-3 rounded-sm" />
            <Skeleton
              className="h-3"
              style={{ width: `${30 + ((i * 19) % 50)}%` }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

export default function SubTrajPage() {
  const { identityId = "", trajId = "" } = useParams();
  const [expandAll, setExpandAll] = useState(false);

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 2000,
  });
  const live = status?.live ?? false;

  const {
    data: traj,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["traj", identityId, trajId],
    queryFn: () => fetchSubTraj(identityId, trajId),
    refetchInterval: pollWhileLive(live),
    // 404 = 轨迹不存在——语义是空态不是故障，重试没有意义。
    retry: (count, err) =>
      !(err instanceof HttpError && err.status === 404) && count < 3,
  });

  const stream = useMemo(
    () => (traj ? assembleStream(traj.steps, traj.runs) : []),
    [traj]
  );

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <SubTrajSkeleton />
      </div>
    );
  }

  // 404 = 轨迹不存在（走"未找到"空态）；其它错误才是故障。
  if (isError && !(error instanceof HttpError && error.status === 404)) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (!traj) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <Empty>
          <EmptyHeader>
            <EmptyTitle>未找到轨迹</EmptyTitle>
            <EmptyDescription>
              身份 {identityId} 下不存在轨迹 {trajId}。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    );
  }

  return (
    <TrajContext.Provider value={{ identityId, trajId: traj.traj_id }}>
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mb-3 flex flex-wrap items-center gap-3">
          <span className="flex flex-wrap items-center gap-1.5 font-mono text-xs text-muted-foreground">
            <Link
              to={`/i/${encodeURIComponent(identityId)}/log`}
              className="hover:text-foreground hover:underline"
            >
              {traj.identity.name}
            </Link>
            {traj.breadcrumb.slice(1).map((crumb) => (
              <Fragment key={crumb.traj_id}>
                <span className="text-faint">/</span>
                {crumb.traj_id === traj.traj_id ? (
                  <span className="font-semibold text-foreground">
                    {crumb.slug.slice(0, 8)}
                  </span>
                ) : (
                  <Link
                    to={`/i/${encodeURIComponent(identityId)}/t/${encodeURIComponent(crumb.traj_id)}`}
                    className="hover:text-foreground hover:underline"
                    title={crumb.slug}
                  >
                    {crumb.slug.slice(0, 8)}
                  </Link>
                )}
              </Fragment>
            ))}
          </span>
          {live && <LiveBadge />}
          <span className="font-mono text-xs text-muted-foreground">
            {traj.step_count} 步
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

        <div className="mb-1 truncate font-mono text-[11px] text-faint">
          {traj.dir_rel}
        </div>

        <div className="mb-4">
          <TimelineBar steps={traj.steps} onStepClick={scrollToStep} />
        </div>

        <div className="flex gap-4">
          <aside className="hidden w-52 shrink-0 md:block">
            <div className="sticky top-28 max-h-[calc(100vh-8rem)] overflow-y-auto pb-4">
              <ForkTree
                identityId={identityId}
                currentTrajId={traj.traj_id}
                live={live}
              />
            </div>
          </aside>
          <div className="min-w-0 flex-1 rounded-xl border border-line bg-card px-2 py-2">
            {stream.length === 0 ? (
              <div className="px-2 py-10 text-center text-sm text-muted-foreground">
                这条子轨迹还没有步骤。
              </div>
            ) : (
              <StreamItems items={stream} expandAll={expandAll} live={live} />
            )}
          </div>
        </div>
      </div>
    </TrajContext.Provider>
  );
}
