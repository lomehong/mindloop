import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";

import { fmtDuration } from "~/components/activity-badge";
import { IdentityTabs } from "~/components/identity-tabs";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchHealth } from "~/lib/api";
import type { ActivityState } from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "Headlong · health" }];
}

const STATE_TEXT: Record<ActivityState, string> = {
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
    <div className="mx-auto w-full max-w-7xl px-4">
      <IdentityTabs
        identityId={identityId}
        live={activity.dispatcher_running}
        active="health"
        name={health.identity.name}
      />
      <div className="space-y-4">
        <Section title="Right now">
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
            <Stat
              label="state"
              value={activity.state}
              className={STATE_TEXT[activity.state]}
            />
            <Stat
              label="steps in flight"
              value={
                activity.steps_in_flight > 0 && activity.busy_thinkers.length
                  ? `${activity.steps_in_flight} (${activity.busy_thinkers.join(", ")})`
                  : String(activity.steps_in_flight)
              }
            />
            <Stat
              label="current run"
              value={fmtDuration(activity.run_seconds) ?? "—"}
            />
            <Stat
              label="last mind-log write"
              value={
                activity.last_step_age_s !== null
                  ? `${fmtDuration(activity.last_step_age_s)} ago`
                  : "—"
              }
              className={
                activity.state === "stalled"
                  ? STATE_TEXT.stalled
                  : undefined
              }
            />
            <Stat
              label="step cadence"
              value={fmtDuration(activity.cadence_s) ?? "—"}
            />
            <Stat
              label="stall threshold"
              value={fmtDuration(activity.stall_after_s) ?? "—"}
            />
          </div>
          {activity.state === "stalled" && (
            <div className="mt-3 text-xs text-amber-600 dark:text-amber-400">
              Busy but the mind log has been quiet past the threshold — a
              step may be hung (a hung step also holds off the dispatcher
              watchdog).
            </div>
          )}
        </Section>

        <Section
          title={`Message queue (${activity.queued_messages.length} waiting · ${activity.pending_total} pending triggers)`}
        >
          {activity.queued_messages.length === 0 ? (
            <div className="text-sm text-muted-foreground">
              Empty — no one is waiting on a reply.
            </div>
          ) : (
            <div className="space-y-1.5">
              {activity.queued_messages.map((message, idx) => (
                <div
                  key={idx}
                  className="flex flex-wrap items-baseline gap-x-2 text-sm"
                >
                  <span className="font-mono text-xs">{message.from ?? "unknown"}</span>
                  <span className="text-xs text-muted-foreground">
                    waiting {fmtDuration(message.age_s) ?? "?"}
                  </span>
                  {message.preview && (
                    <span className="truncate text-xs text-muted-foreground">
                      — {message.preview}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
        </Section>

        <Section
          title={`Responses · last ${responses?.window_days ?? 7} days`}
        >
          {!responses ? (
            <div className="text-sm text-muted-foreground">No mind log.</div>
          ) : (
            <>
              <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
                <Stat label="replied" value={String(responses.replied)} />
                <Stat label="declined" value={String(responses.declined)} />
                <Stat label="undecided" value={String(responses.undecided)} />
                <Stat
                  label="duplicates"
                  value={String(responses.duplicates ?? 0)}
                  className={
                    responses.duplicates
                      ? "text-amber-600 dark:text-amber-400"
                      : undefined
                  }
                />
                <Stat
                  label="median response"
                  value={fmtDuration(responses.median_s) ?? "—"}
                />
                <Stat
                  label="p90"
                  value={fmtDuration(responses.p90_s) ?? "—"}
                />
                <Stat
                  label={`fast path (${responses.paths?.fast.n ?? 0})`}
                  value={fmtDuration(responses.paths?.fast.median_s ?? null) ?? "—"}
                />
                <Stat
                  label={`in-run (${responses.paths?.inline.n ?? 0})`}
                  value={fmtDuration(responses.paths?.inline.median_s ?? null) ?? "—"}
                />
              </div>
              {responses.recent.length > 0 && (
                <table className="mt-3 w-full text-left text-xs">
                  <thead className="text-muted-foreground">
                    <tr>
                      <th className="py-1 pr-4 font-normal">when</th>
                      <th className="py-1 pr-4 font-normal">from</th>
                      <th className="py-1 pr-4 font-normal">outcome</th>
                      <th className="py-1 font-normal">response time</th>
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
                            {event.outcome}
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
          title={`Mid-run injections (${responses?.injections?.length ?? 0})`}
        >
          {!responses?.injections?.length ? (
            <div className="text-sm text-muted-foreground">
              No message has queued behind a busy run in the window.
            </div>
          ) : (
            <>
              <table className="w-full text-left text-xs">
                <thead className="text-muted-foreground">
                  <tr>
                    <th className="py-1 pr-4 font-normal">when</th>
                    <th className="py-1 pr-4 font-normal">from</th>
                    <th className="py-1 pr-4 font-normal">reply via</th>
                    <th className="py-1 pr-4 font-normal">total</th>
                    <th className="py-1 pr-4 font-normal">note written</th>
                    <th className="py-1 pr-4 font-normal">call in flight</th>
                    <th className="py-1 font-normal">reply call</th>
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
                            no reply
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
                A message that arrives mid-run gets a dispatcher note in the
                trajectory; "call in flight" is the wait for the running model
                call to finish, "reply call" the one that composed the answer.
                "fast" means the run ended first and the fast path replied.
              </div>
            </>
          )}
        </Section>

        <Section title="Model calls">
          {!responses?.model?.calls ? (
            <div className="text-sm text-muted-foreground">
              No stamped model calls in the window yet (llm_s lands with the
              observability deploy).
            </div>
          ) : (
            <>
              <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
                <Stat label="calls" value={String(responses.model.calls)} />
                <Stat
                  label="median call"
                  value={fmtDuration(responses.model.llm_p50_s) ?? "—"}
                />
                <Stat
                  label="p90 call"
                  value={fmtDuration(responses.model.llm_p90_s) ?? "—"}
                />
                <Stat
                  label="input tok"
                  value={responses.model.in_tok.toLocaleString()}
                />
                <Stat
                  label="output tok"
                  value={responses.model.out_tok.toLocaleString()}
                />
                <Stat
                  label="thinking tok"
                  value={responses.model.think_tok.toLocaleString()}
                />
              </div>
              {responses.model.daily.length > 0 && (
                <table className="mt-3 w-full text-left text-xs">
                  <thead className="text-muted-foreground">
                    <tr>
                      <th className="py-1 pr-4 font-normal">day (utc)</th>
                      <th className="py-1 pr-4 font-normal">calls</th>
                      <th className="py-1 pr-4 font-normal">input</th>
                      <th className="py-1 pr-4 font-normal">output</th>
                      <th className="py-1 font-normal">thinking</th>
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
