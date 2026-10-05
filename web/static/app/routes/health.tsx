import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";

import { fmtDuration } from "~/components/activity-badge";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { Readout, Ro } from "~/components/readout";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { fetchHealth } from "~/lib/api";
import { formatCount } from "~/lib/format";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 健康" }];
}

const STATE_LABELS: Record<string, string> = {
  working: "工作中",
  stalled: "停滞",
  idle: "空闲",
  asleep: "休眠",
  replied: "已回复",
  declined: "已拒绝",
};

const STATE_DOT: Record<string, string> = {
  working: "bg-primary animate-pulse",
  stalled: "bg-resin",
  idle: "bg-faint",
  asleep: "bg-faint",
};

const STATE_TEXT: Record<string, string> = {
  working: "text-foreground",
  stalled: "text-resin",
  idle: "text-muted-foreground",
  asleep: "text-muted-foreground",
};

/** d-row：当前状态那种「键 → 值」行（值等宽右对齐）。 */
function DRow({
  k,
  v,
  dot,
  dim,
  className,
}: {
  k: string;
  v: string;
  dot?: string;
  dim?: boolean;
  className?: string;
}) {
  return (
    <div className="flex items-center gap-2 rounded-[7px] px-1.5 py-[5.5px] text-[12.5px] text-muted-foreground hover:bg-secondary hover:text-foreground">
      {dot && <span className={cn("size-[7px] flex-none rounded-full", dot)} />}
      <span className="min-w-0 flex-1 truncate">{k}</span>
      <span
        className={cn(
          "font-mono text-[11.5px]",
          dim ? "text-muted-foreground" : "text-foreground",
          className
        )}
      >
        {v}
      </span>
    </div>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-3.5 rounded-xl border border-line bg-card px-4 pb-2.5 pt-3.5">
      <h2 className="mb-0.5 text-[14.5px] font-semibold">{title}</h2>
      {children}
    </div>
  );
}

/** 结果徽标：已回复=叶绿描边、已拒绝=陶土描边、其余=中性描边。 */
function OutcomeChip({ outcome }: { outcome: string }) {
  const label = STATE_LABELS[outcome] ?? outcome;
  const style =
    outcome === "replied"
      ? "border-primary/35 text-primary"
      : outcome === "declined"
        ? "border-clay/35 text-clay"
        : "border-line-strong text-muted-foreground";
  return (
    <span
      className={cn(
        "inline-block rounded-full border px-2 py-px font-mono text-[10.5px]",
        style
      )}
    >
      {label}
    </span>
  );
}

function eventTime(ts: string | null): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const LEDGER_ROW =
  "border-b border-line last:border-b-0 hover:bg-muted px-4 py-2 align-middle";
const LEDGER_HEAD =
  "border-b border-line px-4 py-2 font-mono text-[10.5px] font-normal tracking-[0.1em] text-faint";

export default function HealthPage() {
  const { identityId = "" } = useParams();
  const {
    data: health,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["health", identityId],
    queryFn: () => fetchHealth(identityId),
    refetchInterval: 5000,
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

  if (isLoading || !health) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <div className="mx-auto w-full max-w-4xl space-y-3.5 pb-10">
          <Skeleton className="h-7 w-56" />
          <Skeleton className="h-44 w-full rounded-xl" />
          <Skeleton className="h-44 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  const activity = health.activity;
  const responses = health.responses;

  return (
    <div className="mx-auto w-full max-w-7xl">
      <div className="mx-auto w-full max-w-4xl pb-10">
        <div className="mb-4 flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            健康
          </h1>
          <span className="text-[13px] text-muted-foreground">
            状态 · 回复质量 · 模型调用
          </span>
        </div>

        <Section title="当前状态">
          <div className="pb-1">
            <DRow
              k="状态"
              v={STATE_LABELS[activity.state] ?? activity.state}
              dot={STATE_DOT[activity.state]}
              className={STATE_TEXT[activity.state]}
            />
            <DRow
              k="忙碌思考者"
              v={
                activity.busy_thinkers.length
                  ? activity.busy_thinkers.join(", ")
                  : "—"
              }
            />
            <DRow
              k="当前运行"
              v={fmtDuration(activity.run_seconds) ?? "—"}
            />
            <DRow
              k="最近写入思维日志"
              v={
                activity.last_step_age_s !== null
                  ? `${fmtDuration(activity.last_step_age_s)} 前`
                  : "—"
              }
              className={
                activity.state === "stalled" ? STATE_TEXT.stalled : undefined
              }
            />
            <DRow k="步调节奏" v={fmtDuration(activity.cadence_s) ?? "—"} />
            <DRow
              k="停滞阈值"
              v={fmtDuration(activity.stall_after_s) ?? "—"}
              dim
            />
          </div>
          {activity.state === "stalled" && (
            <div className="mb-1 px-1.5 text-xs text-resin">
              忙碌中但思维日志已超过阈值没有动静——可能有步骤卡住了
              （卡住的步骤也会拖住调度器的看门狗）。
            </div>
          )}
        </Section>

        <Section title={`回复 · 最近 ${responses?.window_days ?? 7} 天`}>
          {!responses ? (
            <div className="py-4 text-sm text-muted-foreground">
              暂无思维日志
            </div>
          ) : (
            <>
              <div className="my-3">
                <Readout>
                  <Ro label="已回复" value={String(responses.replied)} />
                  <Ro label="已拒绝" value={String(responses.declined)} />
                  <Ro label="待处理" value={String(responses.undecided)} />
                  <Ro
                    label="重复回复"
                    value={String(responses.duplicates ?? 0)}
                    className={
                      responses.duplicates ? "text-resin" : undefined
                    }
                  />
                  <Ro
                    label="中位响应"
                    value={fmtDuration(responses.median_s) ?? "—"}
                  />
                  <Ro
                    label="P90 响应"
                    value={fmtDuration(responses.p90_s) ?? "—"}
                  />
                  <Ro
                    label={`快速路径 · ${responses.paths?.fast.n ?? 0}`}
                    value={fmtDuration(responses.paths?.fast.median_s ?? null) ?? "—"}
                  />
                  <Ro
                    label={`运行中回复 · ${responses.paths?.inline.n ?? 0}`}
                    value={
                      fmtDuration(responses.paths?.inline.median_s ?? null) ?? "—"
                    }
                  />
                </Readout>
              </div>
              {responses.recent.length > 0 && (
                <div className="mb-2 overflow-hidden rounded-xl border border-line">
                  <table className="w-full table-fixed text-left text-[13px]">
                    <colgroup>
                      <col className="w-[90px]" />
                      <col />
                      <col className="w-[190px]" />
                      <col className="w-[100px]" />
                    </colgroup>
                    <thead>
                      <tr>
                        <th className={LEDGER_HEAD}>时间</th>
                        <th className={LEDGER_HEAD}>来自</th>
                        <th className={LEDGER_HEAD}>结果</th>
                        <th className={cn(LEDGER_HEAD, "text-right")}>
                          响应耗时
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {responses.recent.map((event, idx) => (
                        <tr key={idx} className={LEDGER_ROW}>
                          <td className="font-mono text-[11.5px] tabular-nums">
                            {eventTime(event.ts)}
                          </td>
                          <td className="truncate pr-2">{event.from}</td>
                          <td>
                            <OutcomeChip outcome={event.outcome} />
                            {event.path && (
                              <span className="ml-1.5 text-xs text-muted-foreground">
                                · {event.path}
                              </span>
                            )}
                          </td>
                          <td className="text-right font-mono tabular-nums">
                            {fmtDuration(event.response_s)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="px-0.5 pb-1.5 text-[12px] leading-relaxed text-muted-foreground">
                「快速」= run 先结束、由快速路径直接回复；插队消息在运行中插入。
              </div>
            </>
          )}
        </Section>

        <Section
          title={`运行中插入消息（${responses?.injections?.length ?? 0}）`}
        >
          {!responses?.injections?.length ? (
            <div className="py-4 text-sm text-muted-foreground">
              窗口期内没有消息在忙碌运行后排队。
            </div>
          ) : (
            <>
              <div className="mb-2 mt-2 overflow-x-auto rounded-xl border border-line">
                <table className="w-full text-left text-[13px]">
                  <thead>
                    <tr>
                      <th className={LEDGER_HEAD}>时间</th>
                      <th className={LEDGER_HEAD}>来自</th>
                      <th className={LEDGER_HEAD}>回复路径</th>
                      <th className={cn(LEDGER_HEAD, "text-right")}>总耗时</th>
                      <th className={cn(LEDGER_HEAD, "text-right")}>
                        写入提示
                      </th>
                      <th className={cn(LEDGER_HEAD, "text-right")}>
                        等待在途调用
                      </th>
                      <th className={cn(LEDGER_HEAD, "text-right")}>回复调用</th>
                    </tr>
                  </thead>
                  <tbody>
                    {responses.injections.map((event, idx) => (
                      <tr key={idx} className={LEDGER_ROW}>
                        <td className="whitespace-nowrap font-mono text-[11.5px] tabular-nums">
                          {eventTime(event.ts)}
                        </td>
                        <td className="max-w-48 truncate pr-2">{event.from}</td>
                        <td>
                          {event.path ? (
                            <span className="font-mono text-[11.5px]">
                              {event.path}
                            </span>
                          ) : (
                            <span className="rounded-full border border-clay/35 px-2 py-px font-mono text-[10.5px] text-clay">
                              未回复
                            </span>
                          )}
                        </td>
                        <td className="text-right font-mono tabular-nums">
                          {fmtDuration(event.total_s) ?? "—"}
                        </td>
                        <td className="text-right font-mono tabular-nums">
                          {event.inject_ms}ms
                        </td>
                        <td className="text-right font-mono tabular-nums">
                          {fmtDuration(event.wait_s) ?? "—"}
                        </td>
                        <td className="text-right font-mono tabular-nums">
                          {fmtDuration(event.model_s) ?? "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="px-0.5 pb-1.5 text-[12px] leading-relaxed text-muted-foreground">
                运行中到达的消息会先在轨迹里写入一条调度器提示；「等待在途调用」
                是等正在运行的模型调用结束的耗时，「回复调用」是组织答复的那次
                调用；「快速」表示 run 先结束、由快速路径直接回复。
              </div>
            </>
          )}
        </Section>

        <Section title="模型调用">
          {!responses?.model?.calls ? (
            <div className="py-4 text-sm text-muted-foreground">
              窗口期内还没有可统计的模型调用（llm_s 指标将随可观测性部署上线）。
            </div>
          ) : (
            <>
              <div className="my-3">
                <Readout>
                  <Ro
                    label="调用次数"
                    value={responses.model.calls.toLocaleString()}
                  />
                  <Ro
                    label="中位调用"
                    value={fmtDuration(responses.model.llm_p50_s) ?? "—"}
                  />
                  <Ro
                    label="P90 调用"
                    value={fmtDuration(responses.model.llm_p90_s) ?? "—"}
                  />
                  <Ro
                    label="输入 TOKEN"
                    value={formatCount(responses.model.in_tok)}
                  />
                  <Ro
                    label="输出 TOKEN"
                    value={formatCount(responses.model.out_tok)}
                  />
                  <Ro
                    label="思考 TOKEN"
                    value={formatCount(responses.model.think_tok)}
                  />
                </Readout>
              </div>
              {responses.model.daily.length > 0 && (
                <div className="mb-2 overflow-hidden rounded-xl border border-line">
                  <table className="w-full text-left text-[13px]">
                    <thead>
                      <tr>
                        <th className={LEDGER_HEAD}>日期（UTC）</th>
                        <th className={cn(LEDGER_HEAD, "text-right")}>调用</th>
                        <th className={cn(LEDGER_HEAD, "text-right")}>输入</th>
                        <th className={cn(LEDGER_HEAD, "text-right")}>输出</th>
                        <th className={cn(LEDGER_HEAD, "text-right")}>思考</th>
                      </tr>
                    </thead>
                    <tbody>
                      {responses.model.daily.map((row) => (
                        <tr key={row.day} className={LEDGER_ROW}>
                          <td className="font-mono text-[11.5px] tabular-nums">
                            {row.day}
                          </td>
                          <td className="text-right font-mono tabular-nums">
                            {row.calls}
                          </td>
                          <td className="text-right font-mono tabular-nums">
                            {row.in_tok.toLocaleString()}
                          </td>
                          <td className="text-right font-mono tabular-nums">
                            {row.out_tok.toLocaleString()}
                          </td>
                          <td className="text-right font-mono tabular-nums">
                            {row.think_tok.toLocaleString()}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </Section>
      </div>
    </div>
  );
}
