/** 舞台对话条（theater.md §3 对话单真相源 · §13.3 在听）：舞台底部
 * 的常驻对话入口——最近气泡摘要 + 输入行 + ⤢ 展开全屏对话。与
 * 对话页/展开态共享同一条 useChat 查询（react-query 同 key 同
 * 缓存），这里不做流式渐进——完整流式体验在展开态，条上是已落
 * 地的对话，诚实呈现不冒充。 */
import { SendHorizontal, Maximize2 } from "lucide-react";
import { useState } from "react";

import { ChatBubble, PendingChatBubble } from "~/components/chat-bubble";
import { Input } from "~/components/ui/input";
import { storedName } from "~/lib/chat-name";
import { useChat } from "~/lib/use-chat";

const STRIP_BUBBLES = 2;

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
  const { messages, pending, send, retry, isSending } = useChat({ identityId, myName });

  const recent = messages.slice(-STRIP_BUBBLES);
  const recentPending = pending.slice(-1);

  return (
    <div
      data-testid="talk-strip"
      className="pointer-events-auto w-full rounded-xl border border-border bg-card/90 shadow-lg backdrop-blur-sm"
    >
      <div className="flex flex-col gap-1 px-3 pt-2">
        {recent.map((message, idx) => (
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
