import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Sparkles } from "lucide-react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchIdentityStatus, fetchRecap, refreshRecap } from "~/lib/api";
import type { RecapStepRef } from "~/lib/types";

export function meta() {
  return [{ title: "mindloop · 摘要" }];
}

/** A step reference that deep-links into the mind log (scroll + highlight). */
function StepRef({
  identityId,
  refItem,
}: {
  identityId: string;
  refItem: RecapStepRef;
}) {
  return (
    <span className="inline-flex items-baseline gap-1">
      <Link
        to={`/i/${encodeURIComponent(identityId)}/mindlog?step=${encodeURIComponent(refItem.step)}`}
        className="rounded bg-muted px-1 font-mono text-[11px] text-primary hover:underline"
        title="在思维日志中打开"
      >
        {refItem.step}
      </Link>
      <span className="text-xs text-muted-foreground">{refItem.note}</span>
    </span>
  );
}

function RefreshButtons({
  identityId,
  refreshing,
  showRebuild = true,
}: {
  identityId: string;
  refreshing: boolean;
  showRebuild?: boolean;
}) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (rebuild: boolean) => refreshRecap(identityId, rebuild),
    onSuccess: (_result, rebuild) => {
      toast.success(
        rebuild
          ? "已开始全量重建——整份日志会在后台重新摘要"
          : "已开始增量刷新——新增步骤会在后台摘要"
      );
      queryClient.invalidateQueries({ queryKey: ["recap", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const busy = refreshing || mutation.isPending;
  return (
    <div className="flex items-center gap-2">
      <Button
        variant="outline"
        size="sm"
        disabled={busy}
        title="增量：只摘要上次摘要以来新增的步骤"
        onClick={() => mutation.mutate(false)}
      >
        <RefreshCw className={`size-3 ${refreshing ? "animate-spin" : ""}`} />
        {refreshing ? "摘要中…" : "刷新"}
      </Button>
      {showRebuild && (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy}
          title="全量重算：丢弃缓存的分集并重新摘要整个日志（更换模型或窗口设置后使用）"
          onClick={() => {
            if (
              window.confirm(
                "从头重建摘要？将丢弃全部缓存分集，并对整份日志重新摘要（每个窗口一次模型调用）。"
              )
            )
              mutation.mutate(true);
          }}
        >
          重建
        </Button>
      )}
    </div>
  );
}

export default function RecapPage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 5000,
  });

  const { data: recap, isLoading } = useQuery({
    queryKey: ["recap", identityId],
    queryFn: () => fetchRecap(identityId),
    refetchInterval: 5000,
  });

  if (isLoading || !recap) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  const header = (
    <IdentityTabs
      identityId={identityId}
      live={status?.live ?? false}
      active="recap"
      name={recap.identity?.name}
    />
  );

  if (!recap.available) {
    return (
      <div className="mx-auto w-full max-w-7xl px-4">
        {header}
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无摘要</EmptyTitle>
            <EmptyDescription>
              {recap.refreshing
                ? "摘要正在生成中——本页会自动刷新。"
                : "为这份思维日志生成 LLM 摘要：主题与情节分集，每一项都能回链到它背后的步骤。"}
            </EmptyDescription>
          </EmptyHeader>
          {controlsEnabled && !recap.refreshing && (
            <RefreshButtons identityId={identityId} refreshing={false} showRebuild={false} />
          )}
          {recap.refreshing && (
            <div className="mt-4 flex justify-center">
              <LoadingDots />
            </div>
          )}
        </Empty>
      </div>
    );
  }

  const themes = recap.themes!;
  const episodes = recap.episodes ?? [];

  return (
    <div className="mx-auto w-full max-w-7xl px-4">
      {header}
      <div className="mx-auto w-full max-w-4xl space-y-8 pb-10">
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-xs text-muted-foreground">
            {episodes.length} 个情节 · 生成于 {themes.generated_at} ·{" "}
            <span className="font-mono">{themes.model}</span>
          </span>
          {(recap.new_steps ?? 0) > 0 && (
            <Badge variant="outline" className="text-[10px]">
              此后新增 {recap.new_steps} 步
            </Badge>
          )}
          {controlsEnabled && (
            <div className="ml-auto">
              <RefreshButtons identityId={identityId} refreshing={recap.refreshing} />
            </div>
          )}
        </div>

        <section>
          <h2 className="mb-2 flex items-center gap-1.5 font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            <Sparkles className="size-3" /> 主线
          </h2>
          <p className="whitespace-pre-line text-sm leading-relaxed">{themes.arc}</p>
        </section>

        <section>
          <h2 className="mb-2 font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            主题
          </h2>
          <div className="space-y-3">
            {themes.themes.map((theme, index) => (
              <div key={index} className="rounded-lg border p-3">
                <div className="mb-1 flex flex-wrap items-baseline gap-2">
                  <span className="text-sm font-medium">{theme.name}</span>
                  <span className="text-[11px] text-muted-foreground">
                    情节 {theme.episodes.join(", ")}
                  </span>
                </div>
                <p className="mb-2 text-sm text-muted-foreground">{theme.description}</p>
                <div className="flex flex-col gap-1">
                  {theme.key_steps?.map((refItem) => (
                    <StepRef key={refItem.step} identityId={identityId} refItem={refItem} />
                  ))}
                </div>
              </div>
            ))}
          </div>
        </section>

        <section>
          <h2 className="mb-2 font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            情节
          </h2>
          <div className="space-y-3">
            {episodes.map((episode) => (
              <div key={episode.idx} className="rounded-lg border p-3">
                <div className="mb-1 flex flex-wrap items-baseline gap-2">
                  <span className="font-mono text-xs text-muted-foreground">
                    {episode.idx}.
                  </span>
                  <span className="text-sm font-medium">{episode.title}</span>
                  {episode.partial && (
                    <Badge variant="outline" className="text-[10px]">
                      不完整
                    </Badge>
                  )}
                  <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                    {episode.first_ts} → {episode.last_ts} · {episode.n_steps} 步
                  </span>
                </div>
                <p className="mb-2 text-sm">{episode.summary}</p>
                <div className="flex flex-col gap-1">
                  {episode.notable_steps?.map((refItem) => (
                    <StepRef key={refItem.step} identityId={identityId} refItem={refItem} />
                  ))}
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
