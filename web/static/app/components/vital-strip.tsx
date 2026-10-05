// 生命线（docs/designs/ui-language.md §4）：每具心智 24 小时的活动痕迹，
// 全站唯一签名元素。柱高=步骤密度；空段=睡眠/未运行（无柱即无活动，
// 不猜）；底端陶土刻度=错误/告警，顶端湖色菱形=消息进出；悬停 scrub
// 跟随读数，点击跳到该时刻的泳道位置。数据一律取自已载入的 mindlog
// 窗口（真实读数，不假造柱）：覆盖 ≥24h 时严格 24h 轴；不足时轴自适应，
// 左缘标注覆盖起点，「加载更早」后自动延长。

import { useMemo, useState } from "react";

import { isErrorStep, stepColor } from "~/lib/step-colors";
import type { NormalizedStep } from "~/lib/types";
import { cn } from "~/lib/utils";

const BUCKETS = 288; // 24h × 5 分钟/桶；自适应轴时等分覆盖时长
const DAY_MS = 24 * 3600_000;
const MSG_TYPES = new Set(["message", "human-msg", "agent-msg"]);

interface Bucket {
  count: number;
  errors: number;
  msgs: number;
  types: string[];
  nearest: NormalizedStep | null;
}

function p2(n: number): string {
  return String(n).padStart(2, "0");
}

function label(ms: number, withDate: boolean): string {
  const d = new Date(ms);
  const time = `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  return withDate ? `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${time}` : time;
}

export function VitalStrip({
  steps,
  live,
  onJump,
  className,
}: {
  steps: NormalizedStep[];
  live: boolean;
  /** 点击某桶 → 跳到该时刻最近的一步（泳道页接 ?step= 深链）。 */
  onJump?: (step: NormalizedStep) => void;
  className?: string;
}) {
  const [hover, setHover] = useState<{
    bucket: number;
    x: number;
    y: number;
  } | null>(null);

  const model = useMemo(() => {
    const times: { step: NormalizedStep; ms: number }[] = [];
    for (const step of steps) {
      const ms = Date.parse(step.ts);
      if (!Number.isNaN(ms)) times.push({ step, ms });
    }
    if (times.length === 0) return null;
    times.sort((a, b) => a.ms - b.ms);
    const firstMs = times[0].ms;
    const lastMs = times[times.length - 1].ms;
    const now = Date.now();
    const axisEnd = live ? Math.max(now, lastMs) : lastMs;
    const coversDay = axisEnd - firstMs >= DAY_MS;
    const axisStart = coversDay ? axisEnd - DAY_MS : firstMs;
    const span = Math.max(1, axisEnd - axisStart);
    const bucketMs = span / BUCKETS;

    const buckets: Bucket[] = Array.from({ length: BUCKETS }, () => ({
      count: 0,
      errors: 0,
      msgs: 0,
      types: [],
      nearest: null,
    }));
    for (const { step, ms } of times) {
      const idx = Math.min(
        BUCKETS - 1,
        Math.max(0, Math.floor((ms - axisStart) / bucketMs))
      );
      const b = buckets[idx];
      b.count += 1;
      if (isErrorStep(step)) b.errors += 1;
      if (MSG_TYPES.has(step.type)) b.msgs += 1;
      if (!b.types.includes(step.type) && b.types.length < 3) b.types.push(step.type);
      // 桶内最近者以 ts 最靠近桶中心为准（ts 升序，后者更近则替换）
      if (!b.nearest) b.nearest = step;
      else {
        const center = axisStart + (idx + 0.5) * bucketMs;
        if (Math.abs(ms - center) <= Math.abs(Date.parse(b.nearest.ts) - center))
          b.nearest = step;
      }
    }
    const maxCount = Math.max(1, ...buckets.map((b) => b.count));
    // 轴标签：左/中/右时刻；跨日时内标带日期
    const crossDay = new Date(axisEnd).getDate() !== new Date(axisStart).getDate();
    const ticks = [0, 0.25, 0.5, 0.75].map((f) =>
      label(axisStart + f * span, crossDay)
    );
    return {
      buckets,
      maxCount,
      axisStart,
      axisEnd,
      span,
      bucketMs,
      coversDay,
      ticks,
      lastMs,
    };
  }, [steps, live]);

  if (!model) return null;
  const { buckets, maxCount, coversDay, axisStart, span, ticks, axisEnd } = model;

  const bucketAt = (clientX: number, el: HTMLElement): number => {
    // 柱行含 px-0.5 内边距与 gap-px：按容器等分会有累计漂移（右缘可达
    // 两桶），必须按实际子元素位置命中，否则悬停/点击错桶。
    const kids = el.firstElementChild?.children;
    if (kids?.length === BUCKETS) {
      const first = kids[0].getBoundingClientRect();
      const last = kids[BUCKETS - 1].getBoundingClientRect();
      const stride = (last.left - first.left) / (BUCKETS - 1);
      if (stride > 0) {
        return Math.min(
          BUCKETS - 1,
          Math.max(0, Math.round((clientX - first.left) / stride))
        );
      }
    }
    const rect = el.getBoundingClientRect();
    const f = (clientX - rect.left) / Math.max(1, rect.width);
    return Math.min(BUCKETS - 1, Math.max(0, Math.floor(f * BUCKETS)));
  };

  const hovered = hover ? buckets[hover.bucket] : null;
  const hoveredStart = hover ? axisStart + hover.bucket * model.bucketMs : 0;

  return (
    <div className={cn("relative", className)}>
      <div
        className="relative h-[34px] cursor-crosshair overflow-hidden rounded-md border border-line"
        style={{
          backgroundImage:
            "linear-gradient(var(--grid) 1px, transparent 1px), linear-gradient(90deg, var(--grid) 1px, transparent 1px)",
          backgroundSize: "100% 17px, 26px 100%",
        }}
        onMouseMove={(e) =>
          setHover({
            bucket: bucketAt(e.clientX, e.currentTarget),
            x: e.clientX,
            y: e.clientY,
          })
        }
        onMouseLeave={() => setHover(null)}
        onClick={(e) => {
          const b = buckets[bucketAt(e.clientX, e.currentTarget)];
          if (b.nearest && onJump) onJump(b.nearest);
        }}
      >
        {/* 柱（苔） */}
        <div className="absolute inset-0 flex items-end gap-px px-0.5">
          {buckets.map((b, i) => (
            <span key={i} className="flex flex-1 items-end self-stretch">
              {b.count > 0 && (
                <span
                  className="w-full rounded-t-[1px] bg-moss opacity-85"
                  style={{
                    height: `${Math.max(8, Math.round((Math.sqrt(b.count) / Math.sqrt(maxCount)) * 92))}%`,
                  }}
                />
              )}
            </span>
          ))}
        </div>
        {/* 错误/告警刻度（陶土）与消息菱形（湖） */}
        {buckets.map((b, i) =>
          b.errors > 0 ? (
            <span
              key={`e${i}`}
              className="absolute bottom-[2px] h-[3px] w-[7px] -translate-x-1/2 rounded-[1px] bg-clay"
              style={{ left: `${((i + 0.5) / BUCKETS) * 100}%` }}
            />
          ) : null
        )}
        {buckets.map((b, i) =>
          b.msgs > 0 ? (
            <span
              key={`m${i}`}
              className="absolute top-[2px] size-[5px] -translate-x-1/2 rotate-45 bg-lake"
              style={{ left: `${((i + 0.5) / BUCKETS) * 100}%` }}
            />
          ) : null
        )}
        {/* 悬停 scrub 指示 */}
        {hover && (
          <span
            className="pointer-events-none absolute inset-y-0 w-px bg-foreground/40"
            style={{ left: `${((hover.bucket + 0.5) / BUCKETS) * 100}%` }}
          />
        )}
        {/* now-edge：右缘叶绿呼吸 */}
        {live && (
          <span
            className="absolute inset-y-0 right-0 w-[2px] animate-pulse bg-primary"
            aria-hidden
          />
        )}
      </div>

      {/* 轴标签 + coverage 说明 */}
      <div className="flex justify-between pt-1 font-mono text-[10px] text-faint">
        <span>
          {coversDay ? "" : "覆盖起点 "}
          {label(axisStart, true)}
        </span>
        {ticks.slice(1).map((t, i) => (
          <span key={i}>{t}</span>
        ))}
        <span>
          {live
            ? `现在 · ${label(axisEnd, true)}`
            : `最后活动 · ${label(axisEnd, true)}`}
        </span>
      </div>

      {/* scrub 读数 */}
      {hover && (
        <div
          className="pointer-events-none fixed z-[60] rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 font-mono text-[11px] shadow-[var(--shadow)]"
          style={{ left: hover.x + 12, top: hover.y - 44 }}
        >
          <span className="text-muted-foreground">
            {label(hoveredStart, true)}
          </span>{" "}
          <span className="font-medium">{hovered?.count ?? 0} 步</span>
          {hovered && hovered.errors > 0 && (
            <span className="text-clay"> · {hovered.errors} 错误</span>
          )}
          {hovered && hovered.msgs > 0 && (
            <span className="text-lake"> · {hovered.msgs} 消息</span>
          )}
          {hovered && hovered.types.length > 0 && (
            <span className="text-muted-foreground"> · {hovered.types.join("/")}</span>
          )}
        </div>
      )}
    </div>
  );
}
