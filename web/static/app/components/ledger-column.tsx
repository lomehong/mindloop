// 成长账单列（avatar.md §6.2 共同成长账的三栏之一）：时间 + 身份署名
// + 单行摘要，至多 ROW_LIMIT 行，更多折叠为「另有 N 条」。工作台同行
// 面与成长档案页共用。

import { formatClock } from "~/lib/format";
import type { LedgerEvent } from "~/lib/growth-ledger";
import { cn } from "~/lib/utils";

export const LEDGER_ROW_LIMIT = 6;

export function LedgerColumn({
  kicker,
  tone,
  events,
  limit = LEDGER_ROW_LIMIT,
}: {
  kicker: string;
  tone: string;
  events: LedgerEvent[];
  limit?: number;
}) {
  const shown = events.slice(0, limit);
  const rest = events.length - shown.length;
  return (
    <div className="min-w-0">
      <div className={cn("mb-1.5 font-mono text-[10.5px] tracking-[0.1em]", tone)}>
        {kicker}
        {events.length > 0 && (
          <span className="ml-1.5 text-faint">{events.length}</span>
        )}
      </div>
      {shown.length === 0 ? (
        <div className="py-1 text-[12.5px] text-faint">今日还没有</div>
      ) : (
        shown.map((ev) => (
          <div
            key={ev.stepId}
            className="flex gap-2 border-b border-border py-1.5 text-[12.5px] last:border-b-0"
          >
            <span className="shrink-0 pt-px font-mono text-[10.5px] text-faint">
              {formatClock(ev.ts).slice(0, 5)}
            </span>
            <span className="shrink-0 pt-px font-mono text-[10.5px] text-muted-foreground">
              {ev.identityName}
            </span>
            <span
              className="min-w-0 flex-1 truncate text-muted-foreground"
              title={ev.summary}
            >
              {ev.summary}
            </span>
          </div>
        ))
      )}
      {rest > 0 && (
        <div className="pt-1 text-[11.5px] text-faint">另有 {rest} 条</div>
      )}
    </div>
  );
}
