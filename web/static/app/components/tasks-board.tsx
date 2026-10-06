// 任务板：等待批准区（执行授权控制面）+ 任务列表——状态、attempt、
// 结果/原因与运行链接都来自任务 API 的投影事实；存在在途任务期间短
// 轮询，全部终态即停手。取消/重试是幂等命令，成功后重取列表而非
// 乐观改写（状态推断权在服务端状态机）。桌面身份「任务」tab 与 PWA
// 任务页共用这一份实现，页面层只提供外壳。

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { toast } from "sonner";

import { ApprovalCard } from "~/components/approval-card";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { TaskCard, isTaskActive } from "~/components/task-card";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  IN_PROGRESS_POLL_MS,
  cancelTask,
  decideApproval,
  fetchApprovals,
  fetchTasks,
  retryTask,
} from "~/lib/api";
import type { AgentTask, PendingApproval } from "~/lib/types";

export function TasksBoard({
  identityId,
  emptyHint,
  focusTaskId,
}: {
  identityId: string;
  /** 空态文案：两端的入口不同（PWA=对话里的「交给 Agent 执行」，
   * 桌面=左栏/页头的「新的任务」）。 */
  emptyHint?: string;
  /** 焦点任务（?task= 深链，来自对话流任务消息/工作卡入口）：滚动到
   * 目标卡并闪烁。 */
  focusTaskId?: string;
}) {
  const queryClient = useQueryClient();

  const query = useQuery({
    queryKey: ["tasks", identityId],
    queryFn: () => fetchTasks(identityId),
    // 有在途任务时短轮询（状态会自己走）；全部终态即停手。
    refetchInterval: (query) =>
      (query.state.data ?? []).some((item) => isTaskActive(item.status))
        ? IN_PROGRESS_POLL_MS
        : false,
  });
  const items = query.data ?? [];

  // 焦点任务（?task= 深链）：任务板渲染后滚动到目标卡并闪烁（tl-flash
  // 与日志流同款标记）。数据未到时重试，找不到（窗口无此任务）静默放弃。
  useEffect(() => {
    if (!focusTaskId || items.length === 0) return;
    let tries = 0;
    let timer: ReturnType<typeof setTimeout>;
    const attempt = () => {
      const el = document.querySelector(
        `[data-task-id="${CSS.escape(focusTaskId)}"]`
      );
      if (el) {
        el.scrollIntoView({ block: "center" });
        el.classList.add("tl-flash");
        setTimeout(() => el.classList.remove("tl-flash"), 3000);
        return;
      }
      if (tries++ < 20) timer = setTimeout(attempt, 250);
    };
    timer = setTimeout(attempt, 100);
    return () => clearTimeout(timer);
  }, [focusTaskId, items.length]);

  // 待批脚本（执行授权控制面）：有待批或有在途任务期间短轮询——
  // 审批会出现也会被心智消费；状态一律以 API 为准。
  const approvalsQuery = useQuery({
    queryKey: ["approvals", identityId],
    queryFn: () => fetchApprovals(identityId),
    refetchInterval: (q) =>
      (q.state.data ?? []).length > 0 ||
      items.some((item) => isTaskActive(item.status))
        ? IN_PROGRESS_POLL_MS
        : false,
  });
  const approvals = approvalsQuery.data ?? [];

  const command = useMutation({
    mutationFn: ({ task, op }: { task: AgentTask; op: "cancel" | "retry" }) =>
      op === "cancel"
        ? cancelTask(identityId, task.task_id)
        : retryTask(identityId, task.task_id),
    onSuccess: (_result, variables) => {
      toast.success(variables.op === "cancel" ? "已请求取消" : "已重新排队");
      void queryClient.invalidateQueries({ queryKey: ["tasks", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const decide = useMutation({
    mutationFn: ({
      approval,
      approve,
    }: {
      approval: PendingApproval;
      approve: boolean;
    }) => decideApproval(identityId, approval.hash, approve),
    onSuccess: (_result, variables) => {
      toast.success(variables.approve ? "已批准" : "已拒绝");
      void queryClient.invalidateQueries({
        queryKey: ["approvals", identityId],
      });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  return (
    <div className="space-y-2">
      {approvals.length > 0 && (
        <div className="space-y-2">
          <h2 className="px-1 text-xs font-medium text-muted-foreground">
            等待批准
          </h2>
          {approvals.map((ap) => (
            <ApprovalCard
              key={ap.hash}
              approval={ap}
              busy={decide.isPending}
              onDecide={(approval, approve) =>
                decide.mutate({ approval, approve })
              }
            />
          ))}
        </div>
      )}
      {query.isLoading ? (
        <div className="flex justify-center py-10">
          <LoadingDots />
        </div>
      ) : query.isError ? (
        <QueryErrorBanner
          error={query.error}
          onRetry={() => void query.refetch()}
        />
      ) : items.length === 0 ? (
        <div className="py-10 text-center text-sm text-muted-foreground">
          {emptyHint ?? "还没有任务。"}
        </div>
      ) : (
        items.map((item) => (
          <TaskCard
            key={item.task_id}
            task={item}
            busy={command.isPending}
            onCancel={(task) => command.mutate({ task, op: "cancel" })}
            onRetry={(task) => command.mutate({ task, op: "retry" })}
          />
        ))
      )}
    </div>
  );
}
