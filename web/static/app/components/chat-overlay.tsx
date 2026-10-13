/** 对话展开态（theater.md §3 对话单真相源）：全屏浮层承载完整对话
 * 能力（目录/历史/流式/深链全在内），Esc 或收起键返回。原 /chat
 * 路由保留（书签/PWA 兜底），同一 ChatPage 组件两种壳——单一真
 * 相源，不做两套对话。身份由路由参数解析（浮层挂在身份页内）。 */
import { useEffect } from "react";

import ChatPage from "~/routes/chat";

export function ChatOverlay({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div
      data-testid="chat-overlay"
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-background/85 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="my-[4vh] mb-[3vh] flex w-[min(860px,88vw)] flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl">
        <div className="flex items-center gap-3 border-b border-border px-5 py-3">
          <span className="text-[13px] font-semibold text-foreground">对话</span>
          <span className="font-mono text-[10px] text-muted-foreground">
            目录 · 历史 · 深链 · 流式回复全在内 · Esc 收起
          </span>
          <button
            type="button"
            data-testid="chat-overlay-close"
            onClick={onClose}
            className="ml-auto rounded-md border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
          >
            收起
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          <ChatPage />
        </div>
      </div>
    </div>
  );
}
