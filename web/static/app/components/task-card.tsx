// 任务卡：显式委托的真实状态呈现——状态徽章、attempt 代次、运行链接、
// 结果或失败原因、来源关联与按状态给出的操作（取消/重试）。状态一律
// 来自任务 API 的事实投影（task.Store），不从 SSE 事件推断。

import { Link } from "react-router";

import { Button } from "~/components/ui/button";
import { formatRelativeTime } from "~/lib/format";
import type { AgentTask, TaskState } from "~/lib/types";
import { cn } from "~/lib/utils";

/** 状态 → 中文标签与配色（叶绿=在正轨、树脂=需留意、陶土=失败、
 * 线色=中性；与健康页结果徽标同一枚芯片语言）。 */
const STATE_META: Record<TaskState, { label: string; className: string }> = {
  queued: { label: "排队中", className: "border-line-strong text-muted-foreground" },
  running: {
    label: "执行中",
    className: "border-primary/35 bg-primary/10 text-primary",
  },
  awaiting_approval: {
    label: "等待确认",
    className: "border-resin/40 bg-resin/10 text-resin",
  },
  canceling: {
    label: "取消中",
    className: "border-line-strong text-muted-foreground",
  },
  succeeded: {
    label: "已完成",
    className: "border-primary/35 text-primary",
  },
  failed: {
    label: "失败",
    className: "border-clay/35 text-clay",
  },
  canceled: { label: "已取消", className: "border-line-strong text-muted-foreground" },
  interrupted: {
    label: "已中断",
    className: "border-resin/40 text-resin",
  },
  budget_exceeded: {
    label: "超出预算",
    className: "border-resin/40 text-resin",
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
    className: "border-line-strong text-muted-foreground",
  };
  return (
    <div
      className="rounded-xl border border-line bg-card p-3"
      data-task-id={task.task_id}
      data-status={task.status}
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "shrink-0 rounded-full border px-2 py-px font-mono text-[10.5px]",
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
        {task.from && task.from !== "operator" && (
          <span className="shrink-0 rounded-full border border-line-strong px-2 py-px font-mono text-[10.5px] text-muted-foreground">
            来自 {task.from}
          </span>
        )}
        {task.source_step_id && (
          <span className="shrink-0 rounded-full border border-line-strong px-2 py-px font-mono text-[10.5px] text-muted-foreground">
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
            to={`/i/${encodeURIComponent(task.identity_id)}/log`}
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
