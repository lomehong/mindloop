// 对话「目录」：长对话的回合大纲（按钮 + 浮层）。回合=一条我方消息 +
// 其后的心智消息（直到下一条我方消息）；心智主动播报（无前置来话）独立
// 成条。点击条目滚动定位 + 闪烁；浮层打开期间滚动联动高亮当前回合。
// 形态取「按钮+浮层」——平时不占版面（2026-10-06 用户裁决）。

import { useEffect, useMemo, useRef, useState } from "react";
import { List } from "lucide-react";

import { dayLabel, localDay } from "~/lib/format";
import type { ChatMessage } from "~/lib/types";
import { cn } from "~/lib/utils";

/** 目录条目：一个回合的锚点。 */
export interface OutlineTurn {
  anchorId: string;
  kind: "mine" | "mind";
  excerpt: string;
  ts: string | null;
  /** 回合中心智消息条数（不含锚点本身）。 */
  replyCount: number;
}

/** 首行摘要（去空行，截断 42 字符）。 */
function firstLine(text: string): string {
  const line = text
    .split("\n")
    .map((l) => l.trim())
    .find((l) => l !== "");
  if (!line) return "（空消息）";
  return line.length > 42 ? line.slice(0, 42) + "…" : line;
}

/** 把消息序列折成回合大纲。 */
export function buildTurns(
  messages: ChatMessage[],
  myName: string
): OutlineTurn[] {
  const isMine = (m: ChatMessage) => m.from === myName || m.from === "you";
  const turns: OutlineTurn[] = [];
  for (const m of messages) {
    if (!m.step_id) continue; // 无锚点不可跳转，不入目录
    if (isMine(m) || turns.length === 0) {
      turns.push({
        anchorId: `msg-${m.step_id}`,
        kind: isMine(m) ? "mine" : "mind",
        excerpt: firstLine(m.content),
        ts: m.ts,
        replyCount: 0,
      });
      continue;
    }
    // 只有开头的心智消息自成一条（turns 为空时）；其余心智消息一律
    // 并入最近回合计数（含心智连发的播报）。
    turns[turns.length - 1].replyCount += 1;
  }
  return turns;
}

/** 目录按钮 + 浮层。scrollerRef 是消息滚动容器（绝对定位的挂靠面）。 */
export function ChatOutline({
  messages,
  myName,
  scrollerRef,
}: {
  messages: ChatMessage[];
  myName: string;
  scrollerRef: { current: HTMLDivElement | null };
}) {
  const turns = useMemo(() => buildTurns(messages, myName), [messages, myName]);
  const [open, setOpen] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  // 浮层打开期间：滚动联动高亮（当前视口顶部最近的回合）。
  useEffect(() => {
    if (!open) return;
    const scroller = scrollerRef.current;
    if (!scroller) return;
    const spy = () => {
      const top = scroller.getBoundingClientRect().top;
      let current: string | null = turns[0]?.anchorId ?? null;
      for (const t of turns) {
        const el = document.getElementById(t.anchorId);
        if (!el) continue;
        if (el.getBoundingClientRect().top - top <= 64) current = t.anchorId;
        else break;
      }
      setActiveId(current);
    };
    spy();
    scroller.addEventListener("scroll", spy, { passive: true });
    return () => scroller.removeEventListener("scroll", spy);
  }, [open, turns, scrollerRef]);

  // Escape 收浮层。
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  const jump = (anchorId: string) => {
    const el = document.getElementById(anchorId);
    setOpen(false);
    if (!el) return;
    el.scrollIntoView({ block: "start" });
    el.classList.add("tl-flash");
    setTimeout(() => el.classList.remove("tl-flash"), 3000);
  };

  // 短对话不配目录（噪声）：至少 5 个回合才出现。
  if (turns.length < 5) return null;

  return (
    <>
      <button
        type="button"
        aria-label="对话目录"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "absolute right-2 top-2 z-20 inline-flex items-center gap-1 rounded-full border border-line bg-card/95 px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground shadow-sm backdrop-blur transition-colors hover:text-foreground",
          open && "text-foreground"
        )}
      >
        <List className="size-3" />
        目录
      </button>
      {open && (
        <>
          <div
            className="absolute inset-0 z-20"
            onClick={() => setOpen(false)}
            aria-hidden
          />
          <div
            ref={panelRef}
            className="absolute right-2 top-10 z-30 max-h-[58vh] w-[320px] overflow-y-auto rounded-xl border border-line bg-card p-2 shadow-lg"
          >
            {turns.map((t, i) => {
              const day = localDay(t.ts);
              const prevDay = i > 0 ? localDay(turns[i - 1].ts) : null;
              const dayRow = day !== null && day !== prevDay;
              return (
                <div key={t.anchorId}>
                  {dayRow && (
                    <div className="px-2 pb-1 pt-2 font-mono text-[10px] uppercase tracking-[0.12em] text-faint">
                      {dayLabel(day!)}
                    </div>
                  )}
                  <button
                    type="button"
                    onClick={() => jump(t.anchorId)}
                    className={cn(
                      "flex w-full items-baseline gap-1.5 rounded-md px-2 py-1 text-left text-xs transition-colors",
                      t.anchorId === activeId
                        ? "bg-accent text-foreground"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
                    )}
                  >
                    <span
                      className={cn(
                        "shrink-0 font-mono text-[10px]",
                        t.kind === "mine" ? "text-primary" : "text-muted-foreground/60"
                      )}
                    >
                      {t.kind === "mine" ? "我" : "它"}
                    </span>
                    <span className="min-w-0 flex-1 truncate">{t.excerpt}</span>
                    {t.replyCount > 0 && (
                      <span className="shrink-0 font-mono text-[10px] text-faint">
                        {t.replyCount}回
                      </span>
                    )}
                  </button>
                  {i === turns.length - 1 && <div className="h-1" />}
                </div>
              );
            })}
          </div>
        </>
      )}
    </>
  );
}
