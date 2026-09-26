// 待批脚本卡：正文、归属、有效期与风险整卡呈现——"执行前展示正文"
// 是 ask 策略的定义。批准/拒绝是服务端决定命令，成功后页面重取列表
// 而非乐观改写（等待方消费决定后状态自己走）。

import { Button } from "~/components/ui/button";
import type { PendingApproval } from "~/lib/types";

/** 展示用的哈希前缀（64 位在手机上不可读）。 */
export function shortApprovalHash(hash: string): string {
  return hash.slice(0, 12);
}

export function ApprovalCard({
  approval,
  busy = false,
  onDecide,
}: {
  approval: PendingApproval;
  /** 决定在途：按钮禁用防重复提交。 */
  busy?: boolean;
  /** approve=true 批准；false 拒绝。 */
  onDecide?: (approval: PendingApproval, approve: boolean) => void;
}) {
  const hhmm = new Date(approval.expires).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
  return (
    <div
      className="rounded-lg border border-amber-300/70 bg-card p-3 dark:border-amber-900"
      data-approval-hash={approval.hash}
    >
      <div className="flex items-center gap-2">
        <span className="shrink-0 rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-900 dark:bg-amber-950 dark:text-amber-200">
          待批准
        </span>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {shortApprovalHash(approval.hash)}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
          有效期至 {hhmm}
        </span>
      </div>
      <div className="mt-1.5 text-xs text-muted-foreground">
        工作目录{" "}
        <span className="break-all font-mono">{approval.work_dir}</span>
        {approval.task_id ? ` · 任务 ${approval.task_id}` : ""}
        {` · 运行 ${approval.run_id}`}
      </div>
      {approval.risks && approval.risks.length > 0 && (
        <div className="mt-1.5 space-y-0.5 text-xs text-amber-700 dark:text-amber-400">
          {approval.risks.map((risk) => (
            <div key={risk}>⚠ {risk}</div>
          ))}
        </div>
      )}
      <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/50 px-2 py-1.5 font-mono text-xs">
        {approval.script}
      </pre>
      <div className="mt-2 flex justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={() => onDecide?.(approval, false)}
        >
          拒绝
        </Button>
        <Button
          size="sm"
          disabled={busy}
          onClick={() => onDecide?.(approval, true)}
        >
          批准
        </Button>
      </div>
    </div>
  );
}
