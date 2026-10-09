import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { QueryErrorBanner } from "~/components/query-error-banner";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { Readout, Ro } from "~/components/readout";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { fetchUsage, refreshUsage } from "~/lib/api";
import { formatBytes, formatClock, formatCount, formatRelativeTime } from "~/lib/format";
import {
  JOB_PROGRESS_POLL_MS,
  USAGE_IDLE_POLL_MS,
} from "~/lib/polling";
import type { UsageAdmission, UsageDay } from "~/lib/types";

export function meta() {
  return [{ title: "mindloop · 用量" }];
}

/** Round a y-axis max up to 1/2/2.5/5 x 10^k（全零→0：只画基线；
 * 小计数→4：刻度 1/2/3/4 都是整数）。 */
function niceMax(v: number): number {
  if (v <= 0) return 0;
  if (v <= 4) return 4;
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

// 分类学配色（ui-language.md §2）：输入=苔(思考)、输出=湖(观察)、思考=梅(心智之言)。
const MOSS = "var(--moss)";
const LAKE = "var(--lake)";
const PLUM = "var(--plum)";
const RESIN = "var(--resin)";

const W = 640;
const H = 220;
const ML = 46;
const MR = 8;
const MT = 10;
const MB = 24;
const PW = W - ML - MR;
const PH = H - MT - MB;

/** 14 天年轮图：格纸底 + 堆叠/分组柱，悬停列出当天每个系列。
 * 纯 SVG，无图表库。 */
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
      <div className="mb-1 flex flex-wrap gap-x-3.5 gap-y-1 font-mono text-[10.5px] text-muted-foreground">
        {series.map((s) => (
          <span key={s.key} className="inline-flex items-center gap-1.5">
            <i
              className="inline-block size-2 rounded-[2px]"
              style={{ background: s.color }}
            />
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
        {ymax > 0 &&
          [0.25, 0.5, 0.75, 1].map((f) => {
            const y = MT + (1 - f) * PH;
            const label = ymax * f;
            return (
              <g key={f}>
                <line
                  x1={ML}
                  y1={y}
                  x2={W - MR}
                  y2={y}
                  style={{ stroke: "var(--grid)" }}
                  strokeWidth={1}
                />
                {Number.isInteger(label) && label >= 1 && (
                  <text
                    x={ML - 6}
                    y={y + 4}
                    textAnchor="end"
                    className="fill-faint font-mono"
                    fontSize={10}
                  >
                    {formatCount(label)}
                  </text>
                )}
              </g>
            );
          })}
        <line
          x1={ML}
          y1={H - MB}
          x2={W - MR}
          y2={H - MB}
          style={{ stroke: "var(--line-strong)" }}
        />
        {days.map(([day], i) =>
          // Regular ticks every labelEvery days; the last day gets one too
          // unless it would sit right next to a regular tick.
          i % labelEvery === 0 || (i === days.length - 1 && i % labelEvery >= 2) ? (
            <text
              key={day}
              x={ML + i * slot + slot / 2}
              y={H - MB + 15}
              textAnchor="middle"
              className="fill-faint font-mono"
              fontSize={10}
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
          className="pointer-events-none absolute z-10 rounded-lg border border-line bg-card px-2.5 py-1.5 font-mono text-[10.5px] shadow-md"
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
            <div className="mt-0.5 flex justify-between gap-4 border-t border-line pt-0.5">
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

/** 图卡：标题 + mono 注记 + 内容（年轮图的容器）。 */
function ChartCard({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-4 rounded-xl border border-line bg-card px-4 pb-3 pt-4">
      <div className="mb-0.5 flex flex-wrap items-baseline gap-2.5">
        <h2 className="text-[14.5px] font-semibold">{title}</h2>
        {note && (
          <span className="font-mono text-[10.5px] text-faint">{note}</span>
        )}
      </div>
      {children}
    </div>
  );
}

const HINT = "px-0.5 text-[12px] leading-relaxed text-muted-foreground";

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

/** 预算水位：今日用量 / 日预算（MINDLOOP_DAILY_TOKENS）。未设置不伪装成
 * 零预算——明说"不设闸"；未知用量如实列出，不冒充零成本。 */
function BudgetMeter({
  admission,
  unknownCalls,
}: {
  admission?: UsageAdmission;
  unknownCalls: number;
}) {
  const limited = !!admission && admission.daily_limit > 0;
  const pct = limited
    ? Math.min(100, (admission.used_today / admission.daily_limit) * 100)
    : 0;
  const metaLeft = !admission
    ? "—"
    : limited
      ? `${admission.used_today.toLocaleString()} / ${admission.daily_limit.toLocaleString()} tokens 今日`
      : "未设置预算 —— 心智不设闸，全部调用落台账";
  const metaRight = !admission ? "—" : limited ? `${pct.toFixed(0)}%` : "0 / ∞";

  const circuit = !admission
    ? "—"
    : admission.cooling_until
      ? `冷却中 · 至 ${formatClock(admission.cooling_until)}（连续 ${admission.consecutive_errors} 次失败；mindloop llm resume 可显式恢复）`
      : admission.consecutive_errors >= admission.circuit_threshold
        ? `等待探测（连续 ${admission.consecutive_errors} 次失败；下一次调用为探测）`
        : "正常";

  return (
    <ChartCard
      title="今日水位"
      note="MINDLOOP_DAILY_TOKENS · UTC 日界重置"
    >
      <div className="px-0.5 pb-1 pt-3">
        <div className="h-[7px] overflow-hidden rounded-full bg-secondary">
          <div
            className="h-full rounded-full bg-primary"
            style={{ width: `${pct}%` }}
          />
        </div>
        <div className="flex justify-between pt-1 font-mono text-[10.5px] text-faint">
          <span>{metaLeft}</span>
          <span className="tabular-nums">{metaRight}</span>
        </div>
      </div>
      <div className={HINT}>
        连续 3 次模型调用失败后进入 5 分钟冷却，冷却结束仅放行一次探测（当前：
        {circuit}）。未知用量 = 供应商未返回 token 计数的调用，它们不计入总量
        {unknownCalls > 0
          ? `（当前 ${unknownCalls.toLocaleString()} 次调用未返回用量）`
          : ""}
        。
      </div>
    </ChartCard>
  );
}

export default function UsagePage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();

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
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading || !usage) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl space-y-3.5 pb-10">
          <Skeleton className="h-7 w-52" />
          <Skeleton className="h-20 w-full rounded-xl" />
          <Skeleton className="h-56 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  if (!usage.available) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
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
    unknown_calls: 0,
  };
  const last7 = days.slice(-7).map(([, v]) => v);
  const n7 = Math.max(1, last7.length);
  const tok7 = last7.reduce((a, v) => a + v.in + v.out + v.think, 0) / n7;
  const calls7 = last7.reduce((a, v) => a + v.calls, 0) / n7;
  const models = Object.entries(usage.by_model ?? {}).sort((a, b) => b[1].in - a[1].in);
  const ledgerSince = usage.ledger?.since ?? null;
  // 年轮图取近 14 天（ui-language.md §C）。
  const chartDays = days.slice(-14);
  const firstDay = days[0]?.[0];
  const ledgerCoversAll = ledgerSince !== null && ledgerSince === firstDay;
  const rangeNote =
    chartDays.length > 0
      ? `${chartDays[0][0].slice(5)} — ${chartDays[chartDays.length - 1][0].slice(5)}`
      : undefined;
  const admission = usage.admission;
  const budgetValue = !admission
    ? { text: "—", unset: true }
    : admission.daily_limit > 0
      ? {
          text: `${formatCount(admission.used_today)} / ${formatCount(admission.daily_limit)}`,
          unset: false,
        }
      : { text: "未设置", unset: true };

  const tokenHint =
    ledgerSince === null
      ? "思维日志 reasoning 步骤上标记的输入/输出/思考 token。目前只统计了代理运行：LLM 用量台账（每次模型调用一行）还没有数据，快速回复与其他思考者的调用暂时缺席。"
      : ledgerCoversAll
        ? "每次模型调用的输入/输出/思考 token（用量台账）：代理运行、快速回复与其他思考者的调用全部覆盖。"
        : `输入/输出/思考 token。${ledgerSince} 起为每次模型调用（用量台账）；此前只有代理运行（token 标在 reasoning 步骤上），那几天的快速回复与其他思考者调用缺席。`;

  return (
    <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
      <div className="mx-auto w-full max-w-4xl pb-10">
        <div className="mb-1.5 flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            用量
          </h1>
          <span className="text-[13px] text-muted-foreground">
            预算 · 台账 · 模型调用
          </span>
          {controlsEnabled && (
            <div className="ml-auto self-center">
              <RefreshButtons
                identityId={identityId}
                refreshing={usage.refreshing}
                label="刷新"
              />
            </div>
          )}
        </div>
        <div className="mb-4 flex flex-wrap items-center gap-2 font-mono text-[10.5px] text-faint">
          <span>
            {usage.rows?.toLocaleString()} 行日志 · 统计于{" "}
            {usage.generated ? formatRelativeTime(usage.generated) : "—"} · 按 UTC 天分组
          </span>
          {usage.pending_bytes > 0 && (
            <Badge variant="outline" className="text-[10px]">
              {formatBytes(usage.pending_bytes)} 待统计
            </Badge>
          )}
        </div>

        <div className="mb-4">
          <Readout>
            <Ro
              label="TOKEN 总量"
              value={formatCount(totals.in + totals.out + totals.think)}
            />
            <Ro label="日均 · 近 7 天" value={formatCount(tok7)} />
            <Ro label="日均模型调用" value={calls7.toFixed(0)} />
            <Ro label="日志覆盖" value={String(days.length)} unit="天" />
            <Ro
              label="今日预算"
              value={budgetValue.text}
              className={budgetValue.unset ? "text-faint" : undefined}
            />
          </Readout>
        </div>

        <ChartCard
          title="每日 Token"
          note={rangeNote ? `${rangeNote} · 输入/输出/思考 分列` : undefined}
        >
          <BarChart
            days={chartDays}
            stacked
            totalLabel="合计"
            series={[
              { key: "in", label: "输入", color: MOSS },
              { key: "out", label: "输出", color: LAKE },
              { key: "think", label: "思考", color: PLUM },
            ]}
          />
          <div className={HINT}>{tokenHint}</div>
        </ChartCard>

        <BudgetMeter
          admission={admission}
          unknownCalls={totals.unknown_calls ?? 0}
        />

        <ChartCard title="模型台账">
          {models.length === 0 ? (
            <p className="py-4 text-sm text-muted-foreground">还没有用量记录。</p>
          ) : (
            <div className="mt-2 overflow-hidden rounded-xl border border-line">
              <table className="w-full text-left text-[13px]">
                <thead>
                  <tr className="border-b border-line font-mono text-[10.5px] tracking-[0.1em] text-faint">
                    <th className="px-4 py-2 font-normal">模型</th>
                    <th className="px-4 py-2 text-right font-normal">调用</th>
                    <th className="px-4 py-2 text-right font-normal">输入</th>
                    <th className="px-4 py-2 text-right font-normal">输出</th>
                    <th className="px-4 py-2 text-right font-normal">思考</th>
                  </tr>
                </thead>
                <tbody>
                  {models.map(([model, v]) => (
                    <tr
                      key={model}
                      className="border-b border-line last:border-b-0 hover:bg-muted"
                    >
                      <td className="px-4 py-2 font-mono text-[12.5px]">
                        {model}
                      </td>
                      <td className="px-4 py-2 text-right font-mono tabular-nums">
                        {v.calls.toLocaleString()}
                      </td>
                      <td className="px-4 py-2 text-right font-mono tabular-nums">
                        {formatCount(v.in)}
                      </td>
                      <td className="px-4 py-2 text-right font-mono tabular-nums">
                        {formatCount(v.out)}
                      </td>
                      <td className="px-4 py-2 text-right font-mono tabular-nums">
                        {formatCount(v.think)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className={HINT}>
            覆盖范围与 token 图相同。台账日期的模型来自每次调用记录；日志日期的模型来自该运行的头行（"?" = 没有 run id 的步骤）。
          </div>
        </ChartCard>

        <div className="grid gap-4 lg:grid-cols-2">
          <ChartCard title="每日消息">
            <BarChart
              days={chartDays}
              stacked={false}
              totalLabel="合计"
              series={[
                { key: "in_msg", label: "收到", color: LAKE },
                { key: "out_msg", label: "发出", color: PLUM },
              ]}
            />
            <div className={HINT}>
              收到的消息 = 其他人发给该身份的消息；发出的消息 = 它自己发出的消息。
            </div>
          </ChartCard>
          <ChartCard title="每日活动">
            <BarChart
              days={chartDays}
              stacked={false}
              series={[
                { key: "calls", label: "模型调用", color: LAKE },
                { key: "runs", label: "启动的运行", color: RESIN },
                { key: "reasoning", label: "reasoning 步骤", color: MOSS },
              ]}
            />
            <div className={HINT}>
              模型调用（覆盖范围与 token 图相同）、启动的代理运行与 reasoning 步骤。
            </div>
          </ChartCard>
        </div>
      </div>
    </div>
  );
}
