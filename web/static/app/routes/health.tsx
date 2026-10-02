import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";

import { fmtDuration } from "~/components/activity-badge";
import { IdentityTabs } from "~/components/identity-tabs";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchHealth } from "~/lib/api";
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

const STATE_TEXT = {
  working: "text-green-600 dark:text-green-400",
  stalled: "text-amber-600 dark:text-amber-400",
  idle: "text-muted-foreground",
  asleep: "text-muted-foreground",
};

function Stat({
  label,
  value,
  className,
}: {
  label: string;
  value: string;
  className?: string;
}) {
  return (
    <div className="min-w-28">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn("font-mono text-lg", className)}>{value}</div>
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
    <div className="rounded-lg border bg-card px-4 py-3">
      <div className="mb-2 text-sm font-medium">{title}</div>
      {children}
    </div>
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

export default function HealthPage() {
  const { identityId = "" } = useParams();
  const { data: health, isLoading } = useQuery({
    queryKey: ["health", identityId],
    queryFn: () => fetchHealth(identityId),
    refetchInterval: 5000,
  });

  if (isLoading || !health) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  const activity = health.activity;
  const responses = health.responses;

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={activity.dispatcher_running}
        active="health"
        name={health.identity.name}
      />
      <div className="space-y-4">
        <Section title="当前状态">
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
            <Stat
              label="状态"
              value={STATE_LABELS[activity.state] ?? activity.state}
              className={STATE_TEXT[activity.state]}
            />
            <Stat
              label="忙碌思考者"
              value={
                activity.busy_thinkers.length
                  ? activity.busy_thinkers.join(", ")
                  : "—"
              }
            />
            <Stat
              label="当前运行"
              value={fmtDuration(activity.run_seconds) ?? "—"}
            />
            <Stat
              label="最近写入思维日志"
              value={
                activity.last_step_age_s !== null
                  ? `${fmtDuration(activity.last_step_age_s)} 前`
                  : "—"
              }
              className={
                activity.state === "stalled"
                  ? STATE_TEXT.stalled
                  : undefined
              }
            />
            <Stat
              label="步调节奏"
              value={fmtDuration(activity.cadence_s) ?? "—"}
            />
            <Stat
              label="停滞阈值"
              value={fmtDuration(activity.stall_after_s) ?? "—"}
            />
          </div>
          {activity.state === "stalled" && (
            <div className="mt-3 text-xs text-amber-600 dark:text-amber-400">
              忙碌中但思维日志已超过阈值没有动静——可能有步骤卡住了
              （卡住的步骤也会拖住调度器的看门狗）。
            </div>
          )}
        </Section>

        <Section
          title={`回复 · 最近 ${responses?.window_days ?? 7} 天`}
        >
          {!responses ? (
            <div className="text-sm text-muted-foreground">暂无思维日志</div>
          ) : (
            <>
              <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
                <Stat label="已回复" value={String(responses.replied)} />
                <Stat label="已拒绝" value={String(responses.declined)} />
                <Stat label="待处理" value={String(responses.undecided)} />
                <Stat
                  label="重复回复"
                  value={String(responses.duplicates ?? 0)}
                  className={
                    responses.duplicates
                      ? "text-amber-600 dark:text-amber-400"
                      : undefined
                  }
                />
                <Stat
                  label="中位响应耗时"
                  value={fmtDuration(responses.median_s) ?? "—"}
                />
                <Stat
                  label="p90 响应"
                  value={fmtDuration(responses.p90_s) ?? "—"}
                />
                <Stat
                  label={`快速路径（${responses.paths?.fast.n ?? 0}）`}
                  value={fmtDuration(responses.paths?.fast.median_s ?? null) ?? "—"}
                />
                <Stat
                  label={`运行中回复（${responses.paths?.inline.n ?? 0}）`}
                  value={fmtDuration(responses.paths?.inline.median_s ?? null) ?? "—"}
                />
              </div>
              {responses.recent.length > 0 && (
                <table className="mt-3 w-full text-left text-xs">
                  <thead className="text-muted-foreground">
                    <tr>
                      <th className="py-1 pr-4 font-normal">时间</th>
                      <th className="py-1 pr-4 font-normal">来自</th>
                      <th className="py-1 pr-4 font-normal">结果</th>
                      <th className="py-1 font-normal">响应耗时</th>
                    </tr>
                  </thead>
                  <tbody className="font-mono">
                    {responses.recent.map((event, idx) => (
                      <tr key={idx} className="border-t">
                        <td className="py-1 pr-4">{eventTime(event.ts)}</td>
                        <td className="max-w-48 truncate py-1 pr-4">
                          {event.from}
                        </td>
                        <td className="py-1 pr-4">
                          <span
                            className={
                              event.outcome === "declined"
                                ? "text-muted-foreground"
                                : undefined
                            }
                          >
                            {STATE_LABELS[event.outcome] ?? event.outcome}
                          </span>
                          {event.path && (
                            <span className="ml-1 text-muted-foreground">
                              · {event.path}
                            </span>
                          )}
                        </td>
                        <td className="py-1">
                          {fmtDuration(event.response_s)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </>
          )}
        </Section>

        <Section
          title={`运行中插入消息（${responses?.injections?.length ?? 0}）`}
        >
          {!responses?.injections?.length ? (
            <div className="text-sm text-muted-foreground">
              窗口期内没有消息在忙碌运行后排队。
            </div>
          ) : (
            <>
              <table className="w-full text-left text-xs">
                <thead className="text-muted-foreground">
                  <tr>
                    <th className="py-1 pr-4 font-normal">时间</th>
                    <th className="py-1 pr-4 font-normal">来自</th>
                    <th className="py-1 pr-4 font-normal">回复路径</th>
                    <th className="py-1 pr-4 font-normal">总耗时</th>
                    <th className="py-1 pr-4 font-normal">写入提示</th>
                    <th className="py-1 pr-4 font-normal">等待在途调用</th>
                    <th className="py-1 font-normal">回复调用</th>
                  </tr>
                </thead>
                <tbody className="font-mono">
                  {responses.injections.map((event, idx) => (
                    <tr key={idx} className="border-t">
                      <td className="py-1 pr-4">{eventTime(event.ts)}</td>
                      <td className="max-w-48 truncate py-1 pr-4">
                        {event.from}
                      </td>
                      <td className="py-1 pr-4">
                        {event.path ?? (
                          <span className="text-amber-600 dark:text-amber-400">
                            未回复
                          </span>
                        )}
                      </td>
                      <td className="py-1 pr-4">
                        {fmtDuration(event.total_s) ?? "—"}
                      </td>
                      <td className="py-1 pr-4">{event.inject_ms}ms</td>
                      <td className="py-1 pr-4">
                        {fmtDuration(event.wait_s) ?? "—"}
                      </td>
                      <td className="py-1">
                        {fmtDuration(event.model_s) ?? "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="mt-2 text-xs text-muted-foreground">
                运行中到达的消息会先在轨迹里写入一条调度器提示；「等待在途调用」
                是等正在运行的模型调用结束的耗时，「回复调用」是组织答复的那次
                调用；「快速」表示 run 先结束、由快速路径直接回复。
              </div>
            </>
          )}
        </Section>

        <Section title="模型调用">
          {!responses?.model?.calls ? (
            <div className="text-sm text-muted-foreground">
              窗口期内还没有可统计的模型调用（llm_s 指标将随可观测性部署上线）。
            </div>
          ) : (
            <>
              <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
                <Stat label="调用次数" value={String(responses.model.calls)} />
                <Stat
                  label="中位调用耗时"
                  value={fmtDuration(responses.model.llm_p50_s) ?? "—"}
                />
                <Stat
                  label="p90 调用耗时"
                  value={fmtDuration(responses.model.llm_p90_s) ?? "—"}
                />
                <Stat
                  label="输入 Token"
                  value={responses.model.in_tok.toLocaleString()}
                />
                <Stat
                  label="输出 Token"
                  value={responses.model.out_tok.toLocaleString()}
                />
                <Stat
                  label="思考 Token"
                  value={responses.model.think_tok.toLocaleString()}
                />
              </div>
              {responses.model.daily.length > 0 && (
                <table className="mt-3 w-full text-left text-xs">
                  <thead className="text-muted-foreground">
                    <tr>
                      <th className="py-1 pr-4 font-normal">日期（UTC）</th>
                      <th className="py-1 pr-4 font-normal">调用</th>
                      <th className="py-1 pr-4 font-normal">输入</th>
                      <th className="py-1 pr-4 font-normal">输出</th>
                      <th className="py-1 font-normal">思考</th>
                    </tr>
                  </thead>
                  <tbody className="font-mono">
                    {responses.model.daily.map((row) => (
                      <tr key={row.day} className="border-t">
                        <td className="py-1 pr-4">{row.day}</td>
                        <td className="py-1 pr-4">{row.calls}</td>
                        <td className="py-1 pr-4">{row.in_tok.toLocaleString()}</td>
                        <td className="py-1 pr-4">{row.out_tok.toLocaleString()}</td>
                        <td className="py-1">{row.think_tok.toLocaleString()}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </>
          )}
        </Section>
      </div>
    </div>
  );
}
