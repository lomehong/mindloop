import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import { fetchIdentityStatus, fetchUsage, refreshUsage } from "~/lib/api";
import type { UsageDay } from "~/lib/types";

export function meta() {
  return [{ title: "Headlong · usage" }];
}

// --- formatting ------------------------------------------------------------

function fmtNum(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1).replace(/\.0$/, "")}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1).replace(/\.0$/, "")}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1).replace(/\.0$/, "")}k`;
  return String(Math.round(n));
}

function fmtBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} MB`;
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`;
  return `${n} B`;
}

function fmtAgo(iso: string): string {
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${(s / 3600).toFixed(1).replace(/\.0$/, "")} h ago`;
  return `${(s / 86400).toFixed(1).replace(/\.0$/, "")} d ago`;
}

/** Round a y-axis max up to 1/2/2.5/5 x 10^k. */
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const mag = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * mag) return m * mag;
  return 10 * mag;
}

// --- bar chart -------------------------------------------------------------

/** The numeric per-day counters a bar series can plot (not `source`). */
type UsageCounter = { [K in keyof UsageDay]: UsageDay[K] extends number ? K : never }[keyof UsageDay];

interface Series {
  key: UsageCounter;
  label: string;
  color: string;
}

const INPUT = "var(--chart-2)";
const OUTPUT = "var(--chart-1)";
const THIRD = "var(--muted-foreground)";

const W = 640;
const H = 220;
const ML = 46;
const MR = 8;
const MT = 10;
const MB = 24;
const PW = W - ML - MR;
const PH = H - MT - MB;

/** Per-day bars (stacked or grouped) with a hover tooltip that lists every
 * series for the day plus an optional total. Plain SVG; no chart library. */
function BarChart({
  days,
  series,
  stacked,
  totalLabel,
}: {
  days: [string, UsageDay][];
  series: Series[];
  stacked: boolean;
  totalLabel?: string;
}) {
  const [hover, setHover] = useState<{ i: number; x: number; y: number } | null>(null);
  const n = Math.max(1, days.length);
  const slot = PW / n;
  const ymax = useMemo(() => {
    let m = 0;
    for (const [, v] of days) {
      if (stacked) m = Math.max(m, series.reduce((a, s) => a + (v[s.key] ?? 0), 0));
      else for (const s of series) m = Math.max(m, v[s.key] ?? 0);
    }
    return niceMax(m);
  }, [days, series, stacked]);
  const labelEvery = Math.max(1, Math.ceil(n / 8));

  // hover.x is in viewBox units (the tooltip's left is a percentage of the
  // chart width, so it tracks the cursor at any rendered size); hover.y is in
  // CSS pixels from the top of the positioned wrapper (legend included).
  const onMove = (ev: React.MouseEvent<SVGSVGElement>) => {
    const rect = ev.currentTarget.getBoundingClientRect();
    const wrapRect = (ev.currentTarget.parentElement ?? ev.currentTarget).getBoundingClientRect();
    const x = ((ev.clientX - rect.left) / rect.width) * W;
    const i = Math.floor((x - ML) / slot);
    if (x < ML || i < 0 || i >= days.length) {
      setHover(null);
      return;
    }
    setHover({ i, x, y: ev.clientY - wrapRect.top });
  };

  const bars: React.ReactNode[] = [];
  days.forEach(([day, v], i) => {
    const x0 = ML + i * slot;
    if (stacked) {
      let acc = 0;
      const bw = Math.max(1, slot * 0.7);
      for (const s of series) {
        const val = v[s.key] ?? 0;
        if (val <= 0) continue;
        const h = (val / ymax) * PH;
        const y = MT + PH - ((acc + val) / ymax) * PH;
        bars.push(
          <rect
            key={`${day}-${s.key}`}
            x={x0 + (slot - bw) / 2}
            y={y}
            width={bw}
            height={h}
            style={{ fill: s.color }}
          />
        );
        acc += val;
      }
    } else {
      const bw = Math.max(1, (slot * 0.8) / series.length);
      series.forEach((s, j) => {
        const val = v[s.key] ?? 0;
        if (val <= 0) return;
        const h = (val / ymax) * PH;
        bars.push(
          <rect
            key={`${day}-${s.key}`}
            x={x0 + slot * 0.1 + j * bw}
            y={MT + PH - h}
            width={bw}
            height={h}
            style={{ fill: s.color }}
          />
        );
      });
    }
  });

  const hovered = hover ? days[hover.i] : null;
  // Right of the cursor; flips to the left past the middle so it stays inside
  // the card.
  const tipLeft = hover ? hover.x + 12 : 0;
  const tipFlip = hover ? hover.x > W * 0.55 : false;

  return (
    <div className="relative">
      <div className="mb-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {series.map((s) => (
          <span key={s.key} className="inline-flex items-center gap-1.5">
            <i className="inline-block size-2.5 rounded-sm" style={{ background: s.color }} />
            {s.label}
          </span>
        ))}
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="block h-auto w-full"
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
        role="img"
      >
        {[0.25, 0.5, 0.75, 1].map((f) => {
          const y = MT + (1 - f) * PH;
          return (
            <g key={f}>
              <line x1={ML} y1={y} x2={W - MR} y2={y} className="stroke-border" strokeWidth={1} />
              <text
                x={ML - 6}
                y={y + 4}
                textAnchor="end"
                className="fill-muted-foreground"
                fontSize={11}
              >
                {fmtNum(ymax * f)}
              </text>
            </g>
          );
        })}
        <line x1={ML} y1={H - MB} x2={W - MR} y2={H - MB} className="stroke-border" />
        {days.map(([day], i) =>
          // Regular ticks every labelEvery days; the last day gets one too
          // unless it would sit right next to a regular tick.
          i % labelEvery === 0 || (i === days.length - 1 && i % labelEvery >= 2) ? (
            <text
              key={day}
              x={ML + i * slot + slot / 2}
              y={H - MB + 15}
              textAnchor="middle"
              className="fill-muted-foreground"
              fontSize={11}
            >
              {day.slice(5)}
            </text>
          ) : null
        )}
        {hover && (
          <rect
            x={ML + hover.i * slot}
            y={MT}
            width={slot}
            height={PH}
            className="fill-foreground/10"
          />
        )}
        {bars}
      </svg>
      {hovered && hover && (
        <div
          className="pointer-events-none absolute z-10 rounded-md border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md"
          style={{
            left: `${(tipLeft / W) * 100}%`,
            top: Math.max(0, hover.y - 8),
            transform: tipFlip ? "translateX(calc(-100% - 24px))" : undefined,
          }}
        >
          <div className="mb-0.5 font-medium">{hovered[0]}</div>
          {series.map((s) => (
            <div key={s.key} className="flex justify-between gap-4">
              <span className="text-muted-foreground">{s.label}</span>
              <span className="tabular-nums">{(hovered[1][s.key] ?? 0).toLocaleString()}</span>
            </div>
          ))}
          {totalLabel && (
            <div className="mt-0.5 flex justify-between gap-4 border-t pt-0.5">
              <span className="text-muted-foreground">{totalLabel}</span>
              <span className="tabular-nums font-medium">
                {series.reduce((a, s) => a + (hovered[1][s.key] ?? 0), 0).toLocaleString()}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// --- page ------------------------------------------------------------------

function Tile({ value, label }: { value: string; label: string }) {
  return (
    <div className="min-w-32 rounded-lg border bg-card px-4 py-3">
      <div className="text-2xl tabular-nums">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

function RefreshButtons({
  identityId,
  refreshing,
  label,
  showRecount = true,
}: {
  identityId: string;
  refreshing: boolean;
  label: string;
  showRecount?: boolean;
}) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (rebuild: boolean) => refreshUsage(identityId, rebuild),
    onSuccess: (_result, rebuild) => {
      toast.success(
        rebuild
          ? "Recount started — the whole mind log and llm ledger are read again in the background"
          : "Usage refresh started — new mind-log and ledger rows get counted in the background"
      );
      queryClient.invalidateQueries({ queryKey: ["usage", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const busy = refreshing || mutation.isPending;
  return (
    <div className="flex items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        onClick={() => mutation.mutate(false)}
        disabled={busy}
        title="Count the rows appended since the last refresh (incremental; the first run reads everything once)"
      >
        <RefreshCw className={`size-3 ${refreshing ? "animate-spin" : ""}`} />
        {refreshing ? "Counting…" : label}
      </Button>
      {showRecount && (
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          title="Discard the cached counts and read the whole mind log and llm ledger again (use after the log was rewritten or curated)"
          onClick={() => {
            if (
              window.confirm(
                "Recount from scratch? The cached counts are discarded and the whole mind log and llm ledger are read again. No LLM calls; takes seconds to a minute on a big log."
              )
            )
              mutation.mutate(true);
          }}
        >
          Recount
        </Button>
      )}
    </div>
  );
}

export default function UsagePage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 5000,
  });

  const { data: usage, isLoading } = useQuery({
    queryKey: ["usage", identityId],
    queryFn: () => fetchUsage(identityId),
    // Cheap (serves a cached file); poll faster while a refresh runs.
    refetchInterval: (query) => (query.state.data?.refreshing ? 1500 : 30000),
  });

  if (isLoading || !usage) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  const header = (
    <IdentityTabs
      identityId={identityId}
      live={status?.live ?? false}
      active="usage"
      name={usage.identity?.name}
    />
  );

  if (!usage.available) {
    return (
      <div className="mx-auto w-full max-w-7xl px-4">
        {header}
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No usage numbers yet</EmptyTitle>
            <EmptyDescription>
              {usage.refreshing
                ? "The mind log is being counted right now — this page refreshes itself."
                : "Count messages, model calls, tokens and runs per day from the mind log. The first pass reads the whole log once; refreshes after that only read what was appended."}
            </EmptyDescription>
          </EmptyHeader>
          {controlsEnabled && !usage.refreshing && (
            <RefreshButtons
              identityId={identityId}
              refreshing={false}
              label="Count usage"
              showRecount={false}
            />
          )}
          {usage.refreshing && (
            <div className="mt-4 flex justify-center">
              <LoadingDots />
            </div>
          )}
        </Empty>
      </div>
    );
  }

  const days = usage.daily ?? [];
  const totals = usage.totals!;
  const last7 = days.slice(-7).map(([, v]) => v);
  const n7 = Math.max(1, last7.length);
  const tok7 = last7.reduce((a, v) => a + v.in + v.out + v.think, 0) / n7;
  const msg7 = last7.reduce((a, v) => a + v.in_msg + v.out_msg, 0) / n7;
  const calls7 = last7.reduce((a, v) => a + v.calls, 0) / n7;
  const models = Object.entries(usage.by_model ?? {}).sort((a, b) => b[1].in - a[1].in);
  const ledgerSince = usage.ledger?.since ?? null;
  const firstDay = days[0]?.[0];
  const ledgerCoversAll = ledgerSince !== null && ledgerSince === firstDay;

  return (
    <div className="mx-auto w-full max-w-7xl px-4">
      {header}
      <div className="space-y-5 pb-10">
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-xs text-muted-foreground">
            {usage.rows?.toLocaleString()} mind-log rows · counted{" "}
            {usage.generated ? fmtAgo(usage.generated) : "—"} · days are UTC
          </span>
          {usage.pending_bytes > 0 && (
            <Badge variant="outline" className="text-[10px]">
              {fmtBytes(usage.pending_bytes)} not counted yet
            </Badge>
          )}
          {controlsEnabled && (
            <div className="ml-auto">
              <RefreshButtons identityId={identityId} refreshing={usage.refreshing} label="Refresh" />
            </div>
          )}
        </div>

        <div className="flex flex-wrap gap-3">
          <Tile value={fmtNum(totals.in + totals.out + totals.think)} label="tokens, all time" />
          <Tile value={fmtNum(tok7)} label="tokens / day, last 7d" />
          <Tile value={msg7.toFixed(0)} label="messages / day, last 7d" />
          <Tile value={calls7.toFixed(0)} label="model calls / day, last 7d" />
          <Tile value={String(days.length)} label="days in the log" />
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Tokens per day</CardTitle>
              <CardDescription>
                {ledgerSince === null
                  ? "Input, output and thinking tokens stamped on shellm reasoning steps. Only shellm runs are counted: the llm usage ledger (every bin/llm call) has no rows yet, so fast-path replies and other thinkers are missing until it does."
                  : ledgerCoversAll
                    ? "Input, output and thinking tokens of every bin/llm call (usage ledger): shellm runs, fast-path replies and other thinkers."
                    : `Input, output and thinking tokens. From ${ledgerSince} on: every bin/llm call (usage ledger). Before that: shellm runs only (tokens stamped on reasoning steps), so fast-path replies and other thinkers are missing on those days.`}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked
                totalLabel="total tokens"
                series={[
                  { key: "in", label: "input", color: INPUT },
                  { key: "out", label: "output", color: OUTPUT },
                  { key: "think", label: "thinking", color: THIRD },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Messages per day</CardTitle>
              <CardDescription>
                Inbound = messages to this identity from anyone else; outbound = its own
                messages out.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked={false}
                totalLabel="total messages"
                series={[
                  { key: "in_msg", label: "inbound", color: INPUT },
                  { key: "out_msg", label: "outbound", color: OUTPUT },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Activity per day</CardTitle>
              <CardDescription>
                Model calls (same coverage as the tokens chart), agentic runs started, and reasoning
                steps.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked={false}
                series={[
                  { key: "calls", label: "model calls", color: INPUT },
                  { key: "runs", label: "runs started", color: OUTPUT },
                  { key: "reasoning", label: "reasoning steps", color: THIRD },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Tokens per model</CardTitle>
              <CardDescription>
                Same coverage as the tokens chart. Ledger days carry the model on each call; on
                mind-log days it comes from the run's shellm-run row ("?" = steps with no run id).
              </CardDescription>
            </CardHeader>
            <CardContent>
              {models.length === 0 ? (
                <p className="text-sm text-muted-foreground">No usage stamped yet.</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>model</TableHead>
                      <TableHead className="text-right">calls</TableHead>
                      <TableHead className="text-right">input</TableHead>
                      <TableHead className="text-right">output</TableHead>
                      <TableHead className="text-right">thinking</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {models.map(([model, v]) => (
                      <TableRow key={model}>
                        <TableCell className="font-mono text-xs">{model}</TableCell>
                        <TableCell className="text-right tabular-nums">
                          {v.calls.toLocaleString()}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {v.in.toLocaleString()}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {v.out.toLocaleString()}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">
                          {v.think.toLocaleString()}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
