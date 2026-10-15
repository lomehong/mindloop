/** 舞台对话条（theater.md §3 对话单真相源 · §13.3 在听）：舞台底部
 * 的常驻对话入口——**悬停展开的气泡日志**（鼠标移开收起到只剩输入
 * 框，不遮挡舞台；移上/聚焦即展开并定位到最新一条）+ 输入行 + ⤢
 * 展开全屏对话。与对话页/展开态共享同一条 useChat 查询（react-query
 * 同 key 同缓存），这里不做流式渐进——完整流式体验在展开态，条上
 * 是已落地的对话，诚实呈现不冒充。 */
import { SendHorizontal, Maximize2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { ChatBubble, PendingChatBubble } from "~/components/chat-bubble";
import { Input } from "~/components/ui/input";
import { storedName } from "~/lib/chat-name";
import { useChat } from "~/lib/use-chat";

export function TalkStrip({
  identityId,
  onExpand,
  onListeningChange,
}: {
  identityId: string;
  onExpand: () => void;
  onListeningChange?: (listening: boolean) => void;
}) {
  const [myName] = useState(() => storedName());
  const [draft, setDraft] = useState("");
  // 悬停/聚焦内展开；移开且焦点不在内则收起。收起是硬切换而非高度
  // 过渡——过渡期间 clientHeight 逐帧变化，scrollTop 会被钳制在中间
  // 值，展开动画结束后停在半空，定位不到最新。
  const [open, setOpen] = useState(false);
  const { messages, pending, send, retry, isSending } = useChat({ identityId, myName });
  const logRef = useRef<HTMLDivElement | null>(null);
  const stickRef = useRef(true);
  const wasOpenRef = useRef(false);

  // 跟底策略：**展开瞬间无条件定位到最新**（收起会卸载日志、滚动
  // 位置不复存在，展开落底部是唯一可预期的行为）；展开期间新消息
  // 仅在用户本就贴近底部时跟随——向上翻历史时不被拽回，贴近与否
  // 由滚动事件实时记账。
  useEffect(() => {
    const log = logRef.current;
    const justOpened = open && !wasOpenRef.current;
    wasOpenRef.current = open;
    if (!log || !open) return;
    if (justOpened || stickRef.current) log.scrollTop = log.scrollHeight;
  }, [messages, pending, open]);

  const recentPending = pending.slice(-1);

  return (
    <div
      data-testid="talk-strip"
      data-open={open ? "true" : "false"}
      className="pointer-events-auto w-full rounded-xl border border-border bg-card/90 shadow-lg backdrop-blur-sm"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
      onFocus={() => setOpen(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setOpen(false);
        }
      }}
    >
      {open && (
        <div
          ref={logRef}
          data-testid="talk-strip-log"
          onScroll={() => {
            const log = logRef.current;
            if (!log) return;
            stickRef.current = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
          }}
          className="flex max-h-[42vh] flex-col gap-1 overflow-y-auto px-3 pt-2"
        >
          {messages.map((message, idx) => (
            <ChatBubble
              key={message.step_id ?? idx}
              message={message}
              mine={message.from === myName || message.from === "you"}
              fromMind={message.from !== myName && message.from !== "you"}
            />
          ))}
          {recentPending.map((message) => (
            <PendingChatBubble
              key={`pending-${message.key}`}
              message={message}
              onRetry={() => retry(message)}
            />
          ))}
        </div>
      )}
      <form
        className="flex items-center gap-2 p-2 pl-3"
        onSubmit={(event) => {
          event.preventDefault();
          if (!draft.trim() || isSending) return;
          send(draft);
          setDraft("");
        }}
      >
        <Input
          data-testid="talk-strip-input"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onFocus={() => onListeningChange?.(true)}
          onBlur={() => onListeningChange?.(false)}
          placeholder="说点什么…（⤢ 展开完整对话）"
          enterKeyHint="send"
          className="h-8 flex-1 border-0 bg-transparent px-1 text-[13px] shadow-none focus-visible:ring-0"
        />
        <button
          type="submit"
          data-testid="talk-strip-send"
          disabled={isSending || !draft.trim()}
          className="flex size-8 items-center justify-center rounded-full border border-primary/40 text-primary transition-colors hover:bg-primary/10 disabled:opacity-40"
          title="发送"
        >
          <SendHorizontal className="size-3.5" />
        </button>
        <button
          type="button"
          data-testid="talk-strip-expand"
          onClick={onExpand}
          className="flex size-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          title="展开对话（对话单真相源）"
        >
          <Maximize2 className="size-3.5" />
        </button>
      </form>
    </div>
  );
}
