// 任务卡：显式委托的真实状态呈现——状态徽章、attempt 代次、运行链接、
// 结果或失败原因、来源关联与按状态给出的操作（取消/重试）。状态一律
// 来自任务 API 的事实投影（task.Store），不从 SSE 事件推断。

import { Link } from "react-router";

import { Button } from "~/components/ui/button";
import { formatRelativeTime } from "~/lib/format";
import type { AgentTask, TaskState } from "~/lib/types";
import { cn } from "~/lib/utils";

/** 状态 → 中文标签与配色（在途态、成功、失败、中断一眼可分）。 */
const STATE_META: Record<TaskState, { label: string; className: string }> = {
  queued: { label: "排队中", className: "bg-muted text-muted-foreground" },
  running: {
    label: "执行中",
    className:
      "bg-blue-100 text-blue-900 dark:bg-blue-950 dark:text-blue-200",
  },
  awaiting_approval: {
    label: "等待确认",
    className:
      "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200",
  },
  canceling: {
    label: "取消中",
    className:
      "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200",
  },
  succeeded: {
    label: "已完成",
    className:
      "bg-green-100 text-green-900 dark:bg-green-950 dark:text-green-200",
  },
  failed: {
    label: "失败",
    className: "bg-red-100 text-red-900 dark:bg-red-950 dark:text-red-200",
  },
  canceled: { label: "已取消", className: "bg-muted text-muted-foreground" },
  interrupted: {
    label: "已中断",
    className:
      "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200",
  },
  budget_exceeded: {
    label: "超出预算",
    className:
      "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200",
  },
};

/** 允许取消的在途状态；终态不提供取消。 */
const CAN_CANCEL: TaskState[] = ["queued", "running", "awaiting_approval"];
/** 允许重试的终态——重试开新一代 attempt，旧证据保留。 */
const CAN_RETRY: TaskState[] = [
  "failed",
  "canceled",
  "interrupted",
  "budget_exceeded",
];

export function taskStateLabel(state: TaskState | string): string {
  return STATE_META[state as TaskState]?.label ?? state;
}

/** 非终态（还在推进）：任务页轮询的依据。 */
export function isTaskActive(state: TaskState | string): boolean {
  return state === "canceling" || CAN_CANCEL.includes(state as TaskState);
}

export function TaskCard({
  task,
  onCancel,
  onRetry,
  busy = false,
}: {
  task: AgentTask;
  onCancel?: (task: AgentTask) => void;
  onRetry?: (task: AgentTask) => void;
  /** 命令在途：按钮禁用防重复提交。 */
  busy?: boolean;
}) {
  const meta = STATE_META[task.status] ?? {
    label: task.status,
    className: "bg-muted text-muted-foreground",
  };
  return (
    <div
      className="rounded-lg border bg-card p-3"
      data-task-id={task.task_id}
      data-status={task.status}
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
            meta.className
          )}
        >
          {meta.label}
        </span>
        {task.attempt > 1 && (
          <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
            第 {task.attempt} 次执行
          </span>
        )}
        {task.source_step_id && (
          <span className="shrink-0 rounded-full border px-2 py-0.5 text-[10px] text-muted-foreground">
            从消息转来
          </span>
        )}
        <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
          {formatRelativeTime(task.updated_at)}
        </span>
      </div>
      <div className="mt-1.5 whitespace-pre-wrap break-words text-sm">
        {task.content}
      </div>
      {task.status === "succeeded" && task.result && (
        <div className="mt-1.5 whitespace-pre-wrap break-words rounded-md bg-muted/50 px-2 py-1.5 text-xs">
          {task.result}
        </div>
      )}
      {task.status !== "succeeded" && task.reason && (
        <div className="mt-1.5 whitespace-pre-wrap break-words text-xs text-muted-foreground">
          {task.reason}
        </div>
      )}
      <div className="mt-2 flex items-center gap-2">
        {task.run_id && (
          <Link
            to={`/i/${encodeURIComponent(task.identity_id)}/mindlog`}
            className="font-mono text-[10px] text-primary underline-offset-4 hover:underline"
          >
            运行记录
          </Link>
        )}
        <span className="ml-auto flex shrink-0 gap-2">
          {CAN_CANCEL.includes(task.status) && onCancel && (
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => onCancel(task)}
            >
              取消
            </Button>
          )}
          {CAN_RETRY.includes(task.status) && onRetry && (
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => onRetry(task)}
            >
              重试
            </Button>
          )}
        </span>
      </div>
    </div>
  );
}
