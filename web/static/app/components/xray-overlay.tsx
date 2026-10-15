// X 光透视（avatar.md §6.1：观测台隐喻的本来含义——透过仪器看同一
// 具身体）。舞台按 X 切换：宇宙压暗 + 格纸 + 中央虚线核心 + 预算大气
// 圈 + 读数卡（思考者/新陈代谢/身体/感官器官）。core 无像素控制——
// 全部读数是既有视图投影（真实 API 值，静默失败显示"读数缺席"），
// 再按 X 或 Esc 返回舞台。

import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";

import {
  fetchHealth,
  fetchIdentities,
  fetchLlmHealth,
  fetchSensors,
  fetchThinkers,
  fetchUsage,
} from "~/lib/api";
import { useNowTicker } from "~/lib/use-chat";
import { formatCount, formatRelativeTime } from "~/lib/format";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import { cn } from "~/lib/utils";

export function XrayOverlay({
  identityId,
  open,
  onClose,
}: {
  identityId: string;
  open: boolean;
  onClose: () => void;
}) {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30_000,
  });
  const live = identities?.find((it) => it.id === identityId)?.live ?? false;
  const { data: thinkers } = useQuery({
    queryKey: ["thinkers", identityId],
    queryFn: () => fetchThinkers(identityId),
    enabled: open && live,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: usage } = useQuery({
    queryKey: ["usage", identityId],
    queryFn: () => fetchUsage(identityId),
    enabled: open,
    refetchInterval: USAGE_IDLE_POLL_MS,
  });
  const { data: health } = useQuery({
    queryKey: ["health", identityId],
    queryFn: () => fetchHealth(identityId),
    enabled: open && live,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: llm } = useQuery({
    queryKey: ["llm-health"],
    queryFn: fetchLlmHealth,
    enabled: open,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const { data: sensors } = useQuery({
    queryKey: ["sensors", identityId],
    queryFn: () => fetchSensors(identityId),
    enabled: open,
    refetchInterval: 60_000,
  });

  // 按 X / Esc 返回舞台（监听在 open 时挂）。
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" || e.key.toLowerCase() === "x") {
        // 输入框里的 x 不算（无输入框，防御性保留）。
        const tag = (e.target as HTMLElement | null)?.tagName ?? "";
        if (/INPUT|TEXTAREA/.test(tag)) return;
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // 冷却到期判定需要"现在"——渲染期不裸调 Date.now()（纯性门禁）。
  const nowTick = useNowTicker(open, 30_000);
  if (!open) return null;

  const admission = usage?.admission;
  const pct =
    admission && admission.daily_limit > 0
      ? Math.round((admission.used_today / admission.daily_limit) * 100)
      : null;
  const today = usage?.daily?.find(
    ([day]) => day === new Date().toISOString().slice(0, 10)
  )?.[1];
  const myLlm = llm?.identities?.find((it) => it.id === identityId);
  const cooling =
    !!admission?.cooling_until && Date.parse(admission.cooling_until) > nowTick;
  const organs = (sensors?.sensors ?? []).filter((s) => s.enabled !== false);
  const ring = pct !== null ? { len: (pct / 100) * 653, total: 653 } : null;

  return (
    <div
      data-testid="xray-overlay"
      className="absolute inset-0 z-30 overflow-hidden bg-background/85 backdrop-blur-[2px]"
    >
      {/* 格纸 + 中央虚线核心 + 预算大气圈 */}
      <div
        aria-hidden
        className="absolute inset-0 opacity-60"
        style={{
          backgroundImage:
            "linear-gradient(var(--grid) 1px, transparent 1px), linear-gradient(90deg, var(--grid) 1px, transparent 1px)",
          backgroundSize: "32px 32px",
        }}
      />
      <div
        aria-hidden
        className="absolute left-1/2 top-1/2 size-32 -translate-x-1/2 -translate-y-1/2 rounded-full border border-dashed border-lake/70"
      />
      {/* 引导虚线（样张 .leader）：核心 → 各读数卡，"透过仪器看同一具身体" */}
      <svg
        aria-hidden
        className="pointer-events-none absolute inset-0 size-full"
        viewBox="0 0 100 100"
        preserveAspectRatio="none"
      >
        {[
          [50, 50, 22, 28],
          [50, 50, 74, 22],
          [50, 50, 20, 70],
          [50, 50, 76, 66],
        ].map(([x1, y1, x2, y2], i) => (
          <line
            key={i}
            x1={x1}
            y1={y1}
            x2={x2}
            y2={y2}
            stroke="var(--line-strong)"
            strokeWidth="0.3"
            strokeDasharray="1 1"
            vectorEffect="non-scaling-stroke"
          />
        ))}
      </svg>
      <svg
        aria-hidden
        className="pointer-events-none absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2"
        width={240}
        height={240}
        viewBox="0 0 240 240"
      >
        <circle cx="120" cy="120" r="104" fill="none" stroke="var(--line)" strokeWidth="2" />
        {ring && (
          <circle
            cx="120"
            cy="120"
            r="104"
            fill="none"
            stroke="var(--resin)"
            strokeWidth="2"
            strokeLinecap="round"
            strokeDasharray={`${ring.len} ${ring.total}`}
            transform="rotate(-90 120 120)"
            opacity="0.85"
          />
        )}
      </svg>
      <div className="absolute left-1/2 top-[calc(50%+84px)] -translate-x-1/2 whitespace-nowrap font-mono text-[10px] text-faint">
        预算大气圈 {pct !== null ? `${pct}%` : "未设预算"}
      </div>

      {/* 思考者读数（左上/右上流式排布） */}
      {(thinkers?.thinkers ?? []).map((t, i) => (
        <Readout
          key={t.name}
          className={i % 2 === 0 ? "left-[7%] top-[18%]" : "right-[7%] top-[14%]"}
          style={{ marginTop: i * 8 }}
          title={`思考者 ${t.name}`}
          lines={[
            `状态 ${t.state} · ${t.types.join("/") || "无类型"}`,
            t.log_mtime ? `上次日志 ${formatRelativeTime(t.log_mtime)}` : "尚无日志",
          ]}
        />
      ))}

      {/* 新陈代谢（左下） */}
      <Readout
        className="bottom-[16%] left-[7%]"
        title="新陈代谢"
        lines={[
          today
            ? `今日 ${formatCount(today.in + today.out + today.think)} tokens · ${today.calls} 次调用`
            : "今日无调用读数",
          pct !== null
            ? `水位 ${pct}%${cooling ? " · 熔断冷却" : pct >= 80 ? " · 微饿" : " — 不饿"}`
            : "未设每日预算",
        ]}
      />

      {/* 身体（右下）：LLM 主档 + 近 1 小时失败 */}
      <Readout
        className="bottom-[16%] right-[7%]"
        title="身体"
        lines={[
          myLlm
            ? `状态 ${llm?.status ?? "unknown"} · 近 1h 失败 ${myLlm.failures_1h} 次`
            : "LLM 健康读数缺席",
          health?.responses
            ? `回复中位 ${health.responses.median_s ?? "—"}s（近 ${health.responses.window_days} 天）`
            : "暂无回复统计",
          cooling ? "疼：熔断冷却中（真的疼）" : "疼觉 0",
        ]}
      />

      {/* 感官器官（顶/底两行流式） */}
      <div className="absolute left-[6%] right-[6%] top-[7%] flex flex-wrap gap-x-5 gap-y-1.5 font-mono text-[11px] text-muted-foreground">
        {organs.length === 0 && <span className="text-faint">没有在线感官</span>}
        {organs.map((s) => (
          <span key={s.id} className="flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-lake/80" />
            <span className="rounded border border-line-strong px-1 text-[10px] text-faint">
              {s.type}
            </span>
            {s.id}
            {s.learning_days ? (
              <span className="text-resin">· 学习期</span>
            ) : null}
          </span>
        ))}
        {sensors?.robotd?.configured && (
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-clay/80" />
            <span className="rounded border border-line-strong px-1 text-[10px] text-faint">身</span>
            robotd · {sensors.robotd.mode === "action" ? "action" : "观察模式（只许看不许动）"}
          </span>
        )}
      </div>
      <div className="absolute bottom-[7%] left-[6%] flex flex-wrap gap-x-5 gap-y-1.5 font-mono text-[11px] text-faint">
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-moss/80" />
          <span className="rounded border border-line-strong px-1 text-[10px]">鼻</span>
          self 内感受 {live ? "· 在线" : "· 随心智停"}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-faint" />
          <span className="rounded border border-line-strong px-1 text-[10px]">意</span>
          思考者 ×{thinkers?.thinkers_total ?? "—"} —— 第六根，本来就有
        </span>
      </div>

      <div className="absolute right-4 top-4 font-mono text-[10.5px] text-faint">
        按 X 返回舞台 · 读数皆既有视图投影
      </div>
    </div>
  );
}

function Readout({
  title,
  lines,
  className,
  style,
}: {
  title: string;
  lines: string[];
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <div
      className={cn(
        "absolute z-10 max-w-72 rounded-lg border border-lake/45 bg-background/80 px-2.5 py-1.5 font-mono text-[11px] leading-relaxed text-lake",
        className
      )}
      style={style}
    >
      <b className="font-normal text-foreground">{title}</b>
      {lines.filter(Boolean).map((line, i) => (
        <div key={i}>{line}</div>
      ))}
    </div>
  );
}
