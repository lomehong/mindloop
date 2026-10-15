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
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-background/70 backdrop-blur-[3px]"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {/* 样张 #chatov 形态（对话单真相源）：全屏沉浸——宇宙压暗、追光
          给对话；消息列 8vw 边距居中，不做居中小卡。 */}
      <div className="flex min-h-0 w-full flex-col">
        <div className="flex items-center gap-3 border-b border-border/60 px-6 py-3">
          <span className="text-[13px] font-semibold text-foreground">对话</span>
          <span className="font-mono text-[10px] text-muted-foreground">
            宇宙压暗 · 追光给对话 · 目录/历史/深链/流式全在内 · Esc 收起
          </span>
          <button
            type="button"
            data-testid="chat-overlay-close"
            onClick={onClose}
            className="ml-auto rounded-md border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
          >
            收起（Esc）
          </button>
        </div>
        {/* ChatPage 自带高度契约（消息区自滚、输入坞贴底）：宿主只
            让位，不再包一层滚动——否则表单跟着内容滚、底部留空腔。 */}
        <div className="mx-auto flex min-h-0 w-full flex-1 flex-col px-[8vw] pb-5 pt-4">
          <ChatPage />
        </div>
      </div>
    </div>
  );
}
