import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { LiveBadge } from "~/components/live-badge";
import { Badge } from "~/components/ui/badge";
import { fetchActivity } from "~/lib/api";
import type { ActivityState, IdentityActivity } from "~/lib/types";
import { cn } from "~/lib/utils";

export function fmtDuration(seconds: number | null): string | null {
  if (seconds === null) return null;
  if (seconds < 90) return `${Math.round(seconds)}s`;
  if (seconds < 5400) return `${Math.round(seconds / 60)}m`;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.round((seconds % 3600) / 60);
  return `${hours}h${String(minutes).padStart(2, "0")}m`;
}

const BADGE_STYLE: Record<ActivityState, string> = {
  working: "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300",
  stalled: "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-300",
  idle: "bg-muted text-muted-foreground",
  asleep: "bg-muted text-muted-foreground",
};

const DOT_STYLE: Record<ActivityState, string> = {
  working: "bg-green-500",
  stalled: "bg-amber-500",
  idle: "bg-muted-foreground/40",
  asleep: "bg-muted-foreground/40",
};

function label(activity: IdentityActivity): string {
  const stepAge = fmtDuration(activity.last_step_age_s);
  const run = fmtDuration(activity.run_seconds);
  switch (activity.state) {
    case "working":
      return [
        "working",
        stepAge ? `step ${stepAge} ago` : null,
        run ? `run ${run}` : null,
      ]
        .filter(Boolean)
        .join(" · ");
    case "stalled":
      return `busy but quiet ${stepAge ?? "?"}`;
    case "idle":
      return "idle";
    case "asleep":
      return "asleep";
  }
}

function tooltip(activity: IdentityActivity): string {
  const lines = [
    `${activity.steps_in_flight} step(s) in flight` +
      (activity.busy_thinkers.length
        ? ` (${activity.busy_thinkers.join(", ")})`
        : ""),
    activity.last_step_ts
      ? `last mind-log write ${fmtDuration(activity.last_step_age_s)} ago`
      : "no mind-log write seen",
    activity.cadence_s !== null
      ? `recent step cadence ~${fmtDuration(activity.cadence_s)}`
      : null,
    activity.state === "stalled"
      ? `quiet past the ${fmtDuration(activity.stall_after_s)} stall threshold`
      : null,
    ...activity.queued_messages.map(
      (m) =>
        `queued ${fmtDuration(m.age_s) ?? "?"}: ${m.from ?? "unknown"}` +
        (m.preview ? ` — ${m.preview.slice(0, 60)}` : "")
    ),
  ];
  return lines.filter(Boolean).join("\n");
}

/** Working / stalled / idle / asleep chip for the identity header. Falls
 * back to the plain live pip until the first activity poll lands. */
export function ActivityBadge({
  identityId,
  live,
}: {
  identityId: string;
  live: boolean;
}) {
  const { data: activity } = useQuery({
    queryKey: ["activity", identityId],
    queryFn: () => fetchActivity(identityId),
    refetchInterval: 2000,
  });

  if (!activity) return live ? <LiveBadge /> : null;

  const queued = activity.queued_messages.length;
  return (
    <Link to={`/i/${encodeURIComponent(identityId)}/health`}>
      <Badge
        className={cn(
          "cursor-pointer gap-1.5 font-normal",
          BADGE_STYLE[activity.state]
        )}
        title={tooltip(activity)}
      >
        <span className="relative flex h-2 w-2">
          {activity.state === "working" && (
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75" />
          )}
          <span
            className={cn(
              "relative inline-flex h-2 w-2 rounded-full",
              DOT_STYLE[activity.state]
            )}
          />
        </span>
        {label(activity)}
        {queued > 0 && ` · ${queued} queued`}
      </Badge>
    </Link>
  );
}
