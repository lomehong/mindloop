import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Play, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  deleteScheduleEntry,
  fetchSchedule,
  runScheduleEntry,
  toggleScheduleEntry,
} from "~/lib/api";
import { SCHEDULE_POLL_MS } from "~/lib/polling";
import type { ScheduleRunResult } from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 日程" }];
}

// kind 徽标文案。
const KIND_LABEL: Record<string, string> = {
  task: "任务",
  exec: "执行",
};

export default function SchedulePage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();
  const queryClient = useQueryClient();
  // 待确认删除的条目 id；null = 对话框关闭。
  const [entryToRemove, setEntryToRemove] = useState<string | null>(null);
  // 每个条目最近一次手动触发的结果：留在卡片上（刷新不打扰）。
  const [runResults, setRunResults] = useState<
    Record<string, ScheduleRunResult>
  >({});

  // 身份切换时清掉上一身份的触发结果（条目 id 可能重名）。
  useEffect(() => {
    setRunResults({});
  }, [identityId]);

  const {
    data: view,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["schedule", identityId],
    queryFn: () => fetchSchedule(identityId),
    refetchInterval: SCHEDULE_POLL_MS,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["schedule", identityId] });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      toggleScheduleEntry(identityId, id, enabled),
    onSuccess: (result) => {
      toast.success(
        result.enabled ? `已启用 ${result.id}` : `已禁用 ${result.id}`
      );
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const run = useMutation({
    mutationFn: (id: string) => runScheduleEntry(identityId, id),
    onSuccess: (result) => {
      setRunResults((prev) => ({ ...prev, [result.id]: result }));
      if (result.ok) {
        toast.success(
          result.kind === "task"
            ? `已提交：${result.note ?? "任务"}`
            : `执行完成：exit ${result.exit_code ?? 0}`
        );
      } else {
        toast.error(result.error ?? "执行失败");
      }
      // exec 会写状态（last_run/last_exit_code），刷新投影。
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteScheduleEntry(identityId, id),
    onSuccess: (result) => {
      toast.success(`已删除 ${result.removed}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <div className="mx-auto w-full max-w-4xl pb-10">
          <QueryErrorBanner error={error} onRetry={() => void refetch()} />
        </div>
      </div>
    );
  }

  if (isLoading || !view) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-28 w-full rounded-xl" />
          <Skeleton className="h-28 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            日程
          </h1>
          <span className="text-[13px] text-muted-foreground">
            schedule.json · 到点由心智心跳触发：task 提交任务、exec 沙箱执行
          </span>
          {controlsEnabled && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto self-center"
              onClick={invalidate}
            >
              <RefreshCw className="size-3" />
              刷新
            </Button>
          )}
        </div>

        {!view.mind_running && (
          <div className="rounded-lg border border-resin/40 bg-resin/[0.06] px-3 py-2 text-xs text-foreground">
            心智未在运行——日程不会被到点触发；「运行」仍可手动触发一次。
          </div>
        )}

        {view.parse_error && (
          <div className="rounded-lg border border-clay/45 bg-clay/[0.06] px-3 py-2 text-xs text-clay">
            <div className="font-medium">
              schedule.json 解析失败——清单不可用、写操作会被拒绝。
            </div>
            <p className="mt-1">{view.parse_error}</p>
            <p className="mt-1 font-mono text-[10px]">{view.file}</p>
          </div>
        )}

        {view.warnings.length > 0 && (
          <div className="rounded-lg border border-resin/40 bg-resin/[0.06] px-3 py-2 text-xs text-resin">
            {view.warnings.map((warning, idx) => (
              <div key={idx}>⚠ {warning}</div>
            ))}
          </div>
        )}

        {view.entries.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CalendarClock className="size-5" />
              </EmptyMedia>
              <EmptyTitle className="text-base">还没有日程条目</EmptyTitle>
              <EmptyDescription>
                <div>
                  用 CLI
                  添加：at/task 到点提交任务，every/exec 周期执行脚本。
                </div>
                <div className="mt-2 break-all rounded-md bg-muted px-3 py-2 text-left font-mono text-[11px]">
                  {`mindloop schedule add ${view.identity.id} '{"id":"daily","at":"21:00","task":"写晚报 {{date}}"}'`}
                </div>
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="rounded-xl border border-line bg-card">
            {view.entries.map((entry) => {
              const result = runResults[entry.id];
              const off = !entry.enabled;
              const dim = off && "opacity-50";
              return (
                <div
                  key={entry.id}
                  className="grid grid-cols-1 items-center gap-x-3 gap-y-1.5 border-b border-line px-4 py-3 last:border-b-0 hover:bg-muted/50 md:grid-cols-[16px_170px_1fr_130px_auto]"
                >
                  <Checkbox
                    checked={entry.enabled}
                    disabled={!controlsEnabled || toggle.isPending}
                    aria-label={`${entry.enabled ? "禁用" : "启用"} ${entry.id}`}
                    onCheckedChange={(checked) =>
                      toggle.mutate({
                        id: entry.id,
                        enabled: checked === true,
                      })
                    }
                  />
                  <div className={cn("flex items-center gap-2", dim)}>
                    <span className="font-mono text-[13px]">{entry.id}</span>
                    <Badge
                      variant="outline"
                      className="rounded-full border-line-strong px-1.5 py-px font-mono text-[9.5px] font-normal text-muted-foreground"
                    >
                      {KIND_LABEL[entry.kind] ?? entry.kind}
                    </Badge>
                  </div>
                  <div
                    className={cn(
                      "text-[12.5px] leading-relaxed text-muted-foreground",
                      dim
                    )}
                  >
                    触发{" "}
                    <span className="font-mono text-[11px] text-foreground">
                      {entry.trigger}
                    </span>
                    <span className="mx-1.5">→</span>
                    动作{" "}
                    <span className="font-mono text-[11px] text-foreground">
                      {entry.action}
                    </span>
                  </div>
                  <div
                    className={cn(
                      "font-mono text-[10.5px] leading-[1.7] text-faint",
                      dim
                    )}
                  >
                    <div>
                      下次{" "}
                      {entry.next_run ?? (entry.enabled ? "—" : "已禁用")}
                    </div>
                    <div>
                      上次 {entry.last_run ?? "—"}
                      {entry.last_exit_code !== undefined &&
                        entry.last_exit_code !== 0 && (
                          <span className="text-clay">
                            {" "}
                            · exit {entry.last_exit_code}
                          </span>
                        )}
                    </div>
                  </div>
                  <div className="flex justify-end gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-6 px-2 text-[11px]"
                      title={`手动运行 ${entry.id}`}
                      aria-label={`手动运行 ${entry.id}`}
                      disabled={!controlsEnabled || run.isPending}
                      onClick={() => run.mutate(entry.id)}
                    >
                      <Play className="size-3" />
                      手动运行
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-[11px] text-muted-foreground hover:text-clay"
                      title={`删除 ${entry.id}`}
                      aria-label={`删除 ${entry.id}`}
                      disabled={!controlsEnabled || remove.isPending}
                      onClick={() => setEntryToRemove(entry.id)}
                    >
                      <Trash2 className="size-3" />
                      删除
                    </Button>
                  </div>
                  {(entry.last_note || entry.last_error || result) && (
                    <div className="col-span-full space-y-0.5 pl-7 font-mono text-[10.5px] md:pl-[calc(16px+12px)]">
                      {entry.last_note && (
                        <div className="text-faint">{entry.last_note}</div>
                      )}
                      {entry.last_error && (
                        <div className="text-clay">{entry.last_error}</div>
                      )}
                      {result && (
                        <div
                          className={cn(
                            result.ok ? "text-primary" : "text-clay"
                          )}
                        >
                          {result.kind === "task"
                            ? `已提交：${result.note ?? "任务"}${
                                result.task_key
                                  ? `（幂等键 ${result.task_key}）`
                                  : ""
                              }`
                            : result.ok
                              ? `执行完成：exit ${result.exit_code ?? 0} · ${
                                  result.duration_ms ?? 0
                                }ms`
                              : `执行失败：${result.error ?? "未知错误"}${
                                  result.detail ? ` — ${result.detail}` : ""
                                }`}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {view.entries.length > 0 && (
          <div className="px-1 text-[12px] leading-relaxed text-muted-foreground">
            条目编辑走 CLI：
            <span className="font-mono text-[11.5px] text-foreground">
              {`mindloop schedule add ${view.identity.id} '{...}'`}
            </span>{" "}
            · 手动运行不影响下次触发时间。
          </div>
        )}
      </div>
      <ConfirmDialog
        open={entryToRemove !== null}
        onOpenChange={(open) => {
          if (!open) setEntryToRemove(null);
        }}
        tone="danger"
        title={entryToRemove ? `删除条目 ${entryToRemove}？` : "删除条目？"}
        description="将从 schedule.json 中移除该条目；已产生的任务与执行日志保留。"
        confirmText="删除"
        onConfirm={() => {
          if (entryToRemove) remove.mutate(entryToRemove);
        }}
      />
    </div>
  );
}
