import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Play, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { IdentityTabs } from "~/components/identity-tabs";
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
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  deleteScheduleEntry,
  fetchIdentityStatus,
  fetchSchedule,
  runScheduleEntry,
  toggleScheduleEntry,
} from "~/lib/api";
import {
  SCHEDULE_POLL_MS,
  STATUS_BACKGROUND_POLL_MS,
} from "~/lib/polling";
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

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const { data: view, isLoading } = useQuery({
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

  if (isLoading || !view) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={status?.live ?? false}
        active="schedule"
        name={view.identity?.name}
      />
      <div className="mx-auto w-full max-w-4xl space-y-6 pb-10">
        <div className="flex items-center gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            日程（schedule.json）
          </h2>
          <span className="text-[11px] text-muted-foreground">
            到点由心智心跳触发：task 提交一个任务、exec
            在沙箱执行。条目编辑走 CLI（mindloop schedule add/remove）。
          </span>
          {controlsEnabled && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              onClick={invalidate}
            >
              <RefreshCw className="size-3" />
              刷新
            </Button>
          )}
        </div>

        {!view.mind_running && (
          <div className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
            心智未在运行——日程不会被到点触发；「运行」仍可手动触发一次。
          </div>
        )}

        {view.parse_error && (
          <div className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-900 dark:border-red-800 dark:bg-red-950 dark:text-red-200">
            <div className="font-medium">
              schedule.json 解析失败——清单不可用、写操作会被拒绝。
            </div>
            <p className="mt-1">{view.parse_error}</p>
            <p className="mt-1 font-mono text-[10px]">{view.file}</p>
          </div>
        )}

        {view.warnings.length > 0 && (
          <div className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
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
                <div className="mt-2 font-mono text-[10px]">{view.file}</div>
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="space-y-3">
            {view.entries.map((entry) => {
              const result = runResults[entry.id];
              return (
                <div
                  key={entry.id}
                  className={cn(
                    "rounded-lg border p-3",
                    !entry.enabled && "opacity-60"
                  )}
                >
                  <div className="flex flex-wrap items-center gap-2">
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
                    <span className="font-mono text-sm font-medium">
                      {entry.id}
                    </span>
                    <Badge variant="outline" className="text-[10px]">
                      {KIND_LABEL[entry.kind] ?? entry.kind}
                    </Badge>
                    <div className="ml-auto flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        title={`手动运行 ${entry.id}`}
                        aria-label={`手动运行 ${entry.id}`}
                        disabled={!controlsEnabled || run.isPending}
                        onClick={() => run.mutate(entry.id)}
                      >
                        <Play className="size-3" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        title={`删除 ${entry.id}`}
                        aria-label={`删除 ${entry.id}`}
                        disabled={!controlsEnabled || remove.isPending}
                        onClick={() => setEntryToRemove(entry.id)}
                      >
                        <Trash2 className="size-3" />
                      </Button>
                    </div>
                  </div>
                  <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span className="font-mono">{entry.trigger}</span>
                    <span className="font-mono">{entry.action}</span>
                  </div>
                  <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    {entry.next_run && <span>下次：{entry.next_run}</span>}
                    {entry.last_run && <span>上次：{entry.last_run}</span>}
                    {entry.last_note && <span>{entry.last_note}</span>}
                    {entry.last_exit_code !== undefined &&
                      entry.last_exit_code !== 0 && (
                        <span className="text-destructive">
                          exit {entry.last_exit_code}
                        </span>
                      )}
                  </div>
                  {entry.last_error && (
                    <p className="mt-1 text-xs text-destructive">
                      {entry.last_error}
                    </p>
                  )}
                  {result && (
                    <p
                      className={cn(
                        "mt-1 text-xs",
                        result.ok
                          ? "text-emerald-600 dark:text-emerald-400"
                          : "text-destructive"
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
                    </p>
                  )}
                </div>
              );
            })}
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
