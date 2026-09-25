import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { ConfirmDialog } from "~/components/confirm-dialog";
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
import { formatBytes, formatCount, formatRelativeTime } from "~/lib/format";
import {
  JOB_PROGRESS_POLL_MS,
  STATUS_BACKGROUND_POLL_MS,
  USAGE_IDLE_POLL_MS,
} from "~/lib/polling";
import type { UsageDay } from "~/lib/types";

export function meta() {
  return [{ title: "mindloop · 用量" }];
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
                {formatCount(ymax * f)}
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
  const [confirmRecount, setConfirmRecount] = useState(false);
  const mutation = useMutation({
    mutationFn: (rebuild: boolean) => refreshUsage(identityId, rebuild),
    onSuccess: (_result, rebuild) => {
      toast.success(
        rebuild
          ? "已开始重算——后台将重新读取整份思维日志与 LLM 账本"
          : "已开始增量刷新——后台统计新增的日志与账本行"
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
        title="统计上次刷新以来新增的行（增量；首次会完整读取一次）"
      >
        <RefreshCw className={`size-3 ${refreshing ? "animate-spin" : ""}`} />
        {refreshing ? "统计中…" : label}
      </Button>
      {showRecount && (
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          title="丢弃缓存计数并重新读取整个思维日志与 LLM 账本（日志被重写或整理后使用）"
          onClick={() => setConfirmRecount(true)}
        >
          重算
        </Button>
      )}
      <ConfirmDialog
        open={confirmRecount}
        onOpenChange={setConfirmRecount}
        title="从头重算？"
        description="将丢弃缓存的计数，重新读取整份思维日志与 LLM 账本。不调用模型；大日志约需数秒到一分钟。"
        confirmText="重算"
        onConfirm={() => mutation.mutate(true)}
      />
    </div>
  );
}

export default function UsagePage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const {
    data: usage,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["usage", identityId],
    queryFn: () => fetchUsage(identityId),
    // Cheap (serves a cached file); poll faster while a refresh runs.
    refetchInterval: (query) =>
      query.state.data?.refreshing ? JOB_PROGRESS_POLL_MS : USAGE_IDLE_POLL_MS,
  });

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <IdentityTabs identityId={identityId} live={false} active="usage" />
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

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
      <div className="mx-auto w-full max-w-7xl">
        {header}
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无用量数据</EmptyTitle>
            <EmptyDescription>
              {usage.refreshing
                ? "思维日志正在统计中——本页会自动刷新。"
                : "从思维日志统计每日的消息、模型调用、token 与运行数。首次统计会完整读取一遍日志；之后的刷新只读取新增部分。"}
            </EmptyDescription>
          </EmptyHeader>
          {controlsEnabled && !usage.refreshing && (
            <RefreshButtons
              identityId={identityId}
              refreshing={false}
              label="统计用量"
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
  // totals 在契约里可选（available=true 不蕴含已算出 totals）——
  // 兜底为零值而不是让整页坠进错误边界。
  const totals = usage.totals ?? {
    in: 0,
    out: 0,
    think: 0,
    calls: 0,
    in_msg: 0,
    out_msg: 0,
    runs: 0,
  };
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
    <div className="mx-auto w-full max-w-7xl">
      {header}
      <div className="space-y-5 pb-10">
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-xs text-muted-foreground">
            {usage.rows?.toLocaleString()} 行日志 · 统计于{" "}
            {usage.generated ? formatRelativeTime(usage.generated) : "—"} · 按 UTC 天分组
          </span>
          {usage.pending_bytes > 0 && (
            <Badge variant="outline" className="text-[10px]">
              {formatBytes(usage.pending_bytes)} 待统计
            </Badge>
          )}
          {controlsEnabled && (
            <div className="ml-auto">
              <RefreshButtons identityId={identityId} refreshing={usage.refreshing} label="刷新" />
            </div>
          )}
        </div>

        <div className="flex flex-wrap gap-3">
          <Tile value={formatCount(totals.in + totals.out + totals.think)} label="token 总量" />
          <Tile value={formatCount(tok7)} label="日均 token（近 7 天）" />
          <Tile value={msg7.toFixed(0)} label="日均消息（近 7 天）" />
          <Tile value={calls7.toFixed(0)} label="日均模型调用（近 7 天）" />
          <Tile value={String(days.length)} label="日志覆盖天数" />
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">每日 Token</CardTitle>
              <CardDescription>
                {ledgerSince === null
                  ? "思维日志 reasoning 步骤上标记的输入/输出/思考 token。目前只统计了代理运行：LLM 用量台账（每次模型调用一行）还没有数据，快速回复与其他思考者的调用暂时缺席。"
                  : ledgerCoversAll
                    ? "每次模型调用的输入/输出/思考 token（用量台账）：代理运行、快速回复与其他思考者的调用全部覆盖。"
                    : `输入/输出/思考 token。${ledgerSince} 起为每次模型调用（用量台账）；此前只有代理运行（token 标在 reasoning 步骤上），那几天的快速回复与其他思考者调用缺席。`}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked
                totalLabel="合计"
                series={[
                  { key: "in", label: "输入", color: INPUT },
                  { key: "out", label: "输出", color: OUTPUT },
                  { key: "think", label: "思考", color: THIRD },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">每日消息</CardTitle>
              <CardDescription>
                收到的消息 = 其他人发给该身份的消息；发出的消息 = 它自己发出的消息。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked={false}
                totalLabel="合计"
                series={[
                  { key: "in_msg", label: "收到", color: INPUT },
                  { key: "out_msg", label: "发出", color: OUTPUT },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">每日活动</CardTitle>
              <CardDescription>
                模型调用（覆盖范围与 token 图相同）、启动的代理运行与 reasoning 步骤。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BarChart
                days={days}
                stacked={false}
                series={[
                  { key: "calls", label: "模型调用", color: INPUT },
                  { key: "runs", label: "启动的运行", color: OUTPUT },
                  { key: "reasoning", label: "reasoning 步骤", color: THIRD },
                ]}
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">各模型的 Token</CardTitle>
              <CardDescription>
                覆盖范围与 token 图相同。台账日期的模型来自每次调用记录；日志日期的模型来自该运行的头行（"?" = 没有 run id 的步骤）。
              </CardDescription>
            </CardHeader>
            <CardContent>
              {models.length === 0 ? (
                <p className="text-sm text-muted-foreground">还没有用量记录。</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>模型</TableHead>
                      <TableHead className="text-right">调用</TableHead>
                      <TableHead className="text-right">输入</TableHead>
                      <TableHead className="text-right">输出</TableHead>
                      <TableHead className="text-right">思考</TableHead>
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
