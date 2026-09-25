// 实时活动进度卡：心智接了任务去干活期间，在消息线程底部回答三个问题
// ——在做什么（当前阶段行）、进行到哪（已完成 N 步）、干了多久（mm:ss
// 计时器）。数据来自 /replies/stream 的 working/step 事件 + 最近一次发
// 送时间戳。working=false 后由 hook 延迟清空 activity，本卡以透明度过
// 渡播完淡出再卸载（见 use-chat.ts 的 ACTIVITY_LINGER_MS）。

import { useEffect, useState } from "react";

import type { StepActivity } from "~/lib/use-chat";
import { formatRelativeTime } from "~/lib/format";
import { cn } from "~/lib/utils";

// 步骤类型 → 图标与中文标签；未收录的类型显示原文，协议演进不用改这。
const STEP_META: Record<string, { icon: string; label: string }> = {
  reasoning: { icon: "💭", label: "思考" },
  thought: { icon: "💭", label: "思考" },
  action: { icon: "⚙️", label: "执行命令" },
  "shell-output": { icon: "🖥", label: "命令输出" },
  observation: { icon: "📄", label: "观察" },
  final: { icon: "✅", label: "完成" },
  error: { icon: "❌", label: "出错" },
};

// 「最近 step」的新鲜度阈值：超过则视为模型在长推理，显性化以免像卡死。
const STALE_STEP_MS = 5000;
// 长任务提示阈值。
const LONG_TASK_MS = 60_000;

/** 每秒心跳：mm:ss 计时器与阶段时长都吃这条 tick。只在卡片挂载期运行；
 * interval 回调里 setState，effect 体保持干净；激活瞬间经 0ms 定时器先
 * 校准一次，避免第一秒显示冻结的旧值。 */
function useNowTicker(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const initial = setTimeout(() => setNow(Date.now()), 0);
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => {
      clearTimeout(initial);
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

/** 毫秒 → mm:ss（分钟两位补零）。 */
export function formatMmSs(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const mm = String(Math.floor(total / 60)).padStart(2, "0");
  const ss = String(total % 60).padStart(2, "0");
  return `${mm}:${ss}`;
}

export function WorkingCard({
  name,
  working,
  activity,
  stepTotal,
  sentAt,
  variant = "desktop",
}: {
  /** 身份显示名（标题里的「谁」）。 */
  name: string;
  working: boolean;
  activity: StepActivity[];
  /** 本次任务累计步数（自最近一次发送起算）。 */
  stepTotal: number;
  /** 最近一次发送的时间戳；耗时计时基准。null（无发送的自发活动）时
   * 不显示计时器。 */
  sentAt: number | null;
  variant?: "desktop" | "talk";
}) {
  const now = useNowTicker();
  // 无活可干且淡出已清空：整卡退场。
  if (!working && activity.length === 0) return null;
  const talk = variant === "talk";

  const elapsed = sentAt !== null ? Math.max(0, now - sentAt) : null;
  const longTask = elapsed !== null && elapsed >= LONG_TASK_MS;

  // 阶段行：最近 step 事件 5s 内 → 显示该步骤（图标+类型+摘要）；超过 5s
  // 无新步骤且仍在工作 → 显性化「深度思考中」，避免长推理期像卡死。
  const last = activity[activity.length - 1];
  const lastAge = last ? Math.max(0, now - last.arrivedAt) : null;
  const stageThinking =
    working && (last === undefined || (lastAge ?? 0) >= STALE_STEP_MS);
  // 深度思考已持续的秒数：有步骤时从最近步骤起算，尚无步骤时从发送起算。
  const thinkingSince = last !== null && last !== undefined ? lastAge : elapsed;
  // 最新步骤已在阶段行展示：历史列表剔除它，避免同一行出现两次。
  const history = stageThinking || last === undefined
    ? activity
    : activity.slice(0, -1);

  return (
    <div
      className={cn(
        "rounded-lg border bg-card transition-opacity duration-300",
        talk ? "max-w-[85%] px-3 py-2" : "px-3 py-2.5",
        working ? "opacity-100" : "opacity-0"
      )}
      data-working={working ? "true" : "false"}
    >
      <div className="flex items-center gap-2">
        <span className="relative flex h-2 w-2 shrink-0">
          {working && (
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75" />
          )}
          <span
            className={cn(
              "relative inline-flex h-2 w-2 rounded-full",
              working ? "bg-green-500" : "bg-muted-foreground/40"
            )}
          />
        </span>
        <span className="text-sm font-medium">{name} 正在工作中…</span>
        {elapsed !== null && (
          <span className="font-mono text-xs tabular-nums text-muted-foreground">
            {formatMmSs(elapsed)}
          </span>
        )}
        <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
          已完成 {stepTotal} 步
        </span>
      </div>
      {longTask && (
        <div className="mt-1 text-[11px] text-muted-foreground">
          长任务可能需要几分钟，完成后会回复。
        </div>
      )}
      {(stageThinking || last !== undefined) && (
        <div className="mt-1.5 flex items-baseline gap-1.5 text-xs text-muted-foreground">
          {stageThinking ? (
            <span className="min-w-0 flex-1 truncate">
              💭 深度思考中（已{" "}
              {Math.max(0, Math.floor((thinkingSince ?? 0) / 1000))}s）——模型
              推理不产生步骤
            </span>
          ) : (
            last && (
              <>
                <span aria-hidden>{STEP_META[last.stepType]?.icon ?? "•"}</span>
                <span className="shrink-0 font-medium text-foreground/80">
                  {STEP_META[last.stepType]?.label ?? last.stepType}
                </span>
                <span className="min-w-0 flex-1 truncate">
                  {last.excerpt || last.stepType}
                </span>
              </>
            )
          )}
        </div>
      )}
      {history.length > 1 && (
        <ul className="mt-1.5 space-y-1">
          {history.map((step) => {
            const meta = STEP_META[step.stepType];
            return (
              <li
                key={step.stepId}
                className="flex items-baseline gap-1.5 text-xs text-muted-foreground"
              >
                <span aria-hidden>{meta?.icon ?? "•"}</span>
                <span className="shrink-0 font-medium text-foreground/80">
                  {meta?.label ?? step.stepType}
                </span>
                <span className="min-w-0 flex-1 truncate">
                  {step.excerpt || step.stepType}
                </span>
                <span className="shrink-0 font-mono text-[10px]">
                  {formatRelativeTime(step.ts)}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
