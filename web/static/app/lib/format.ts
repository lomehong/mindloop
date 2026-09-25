// Shared formatting helpers. Consolidated from near-duplicate copies that
// used to live in routes/usage.tsx, routes/config.tsx, routes/thinkers.tsx,
// routes/home.tsx, components/timeline-view.tsx and lib/timeline-model.ts.
//
// 口径约定（统一并注明，勿在各调用点私自再变体）：
// - 字节数走二进制（1024 进位：B → KB → MB → GB），与操作系统对文件
//   大小的惯例一致。
// - 大数计数走十进制缩写（k / M / B），与图表坐标轴惯例一致。
// - 相对时间中文表述，阈值统一：刚刚 / N 分钟前 / N 小时前 / N 天前。

/** Compact decimal count: 1.2k / 3.4M / 5.6B（图表轴、汇总瓦片用）。 */
export function formatCount(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1).replace(/\.0$/, "")}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1).replace(/\.0$/, "")}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1).replace(/\.0$/, "")}k`;
  return String(Math.round(n));
}

/** Binary bytes: 512 B / 12.3 KB / 4.5 MB / 1.2 GB。 */
export function formatBytes(n: number): string {
  if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(1)} GB`;
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

/** 中文相对时间；null/undefined（后端未知）渲染为破折号。 */
export function formatRelativeTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const seconds = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
  if (Number.isNaN(seconds)) return "—";
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

/** Wall-clock span between two ISO timestamps ("3m 5s"); null when either
 * side is missing/invalid. */
export function formatDuration(
  started: string | null,
  ended: string | null
): string | null {
  if (!started || !ended) return null;
  const ms = Date.parse(ended) - Date.parse(started);
  return formatSpan(ms);
}

/** A duration in milliseconds as a compact span ("45s", "12m 4s", "3h 2m");
 * null/NaN/negative input renders as null（调用点据此回退默认文案）。 */
export function formatSpan(ms: number): string | null {
  if (!Number.isFinite(ms) || ms < 0) return null;
  const s = Math.round(ms / 1000);
  if (s < 90) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 90) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

/** ISO 时间戳 → 当前时区 wall clock（HH:MM:SS）。后端时间恒为 UTC ISO，
 * 必须经 Date 解析转换本地时区——直接 slice 原始字符串会显示 UTC。
 * 解析失败（非法/手工时间戳）回退原文。 */
export function formatClock(ts: string): string {
  const ms = Date.parse(ts);
  if (Number.isNaN(ms)) return ts.length >= 19 ? ts.slice(11, 19) : ts;
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/** ISO 时间戳 → 当前时区日期时间（YYYY-MM-DD HH:MM）。解析失败回退原文。 */
export function formatDateTime(ts: string): string {
  const ms = Date.parse(ts);
  if (Number.isNaN(ms)) return ts;
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
