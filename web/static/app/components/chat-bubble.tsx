// 聊天气泡的单一实现：桌面（/i/:id/chat）与 PWA（/talk/:id）共用。
// variant 只承载两端有意的视觉/信息密度差异——桌面显示 from→to 头部、
// 附件名与 Slack 链接，正文纯文本；PWA 对收到的消息渲染 Markdown、
// 时间靠右、圆角更大。mine 判定、时间格式化、乐观气泡与失败重试
// 只有这一份实现。

import { ExternalLink } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import type { PendingMessage } from "~/lib/use-chat";
import type { ChatMessage } from "~/lib/types";
import { slackConversationUrl, slackSourceUrl } from "~/lib/source-links";
import { cn } from "~/lib/utils";

export type ChatBubbleVariant = "desktop" | "talk";

export function messageTime(ts: string | null): string {
  if (!ts) return "";
  const date = new Date(ts);
  // 解析失败时回退显示原文，好过悄悄消失。
  if (Number.isNaN(date.getTime())) return ts;
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function ChatBubble({
  message,
  mine,
  variant = "desktop",
  onConvertToTask,
}: {
  message: ChatMessage;
  mine: boolean;
  variant?: ChatBubbleVariant;
  /** 提供时，自己的消息（有 step_id）显示「转为任务」入口——以该步
   * 骤为 source_step_id 提交显式委托，保留来源关联。 */
  onConvertToTask?: () => void;
}) {
  const talk = variant === "talk";
  const sourceUrl =
    slackSourceUrl(message.source_url) ||
    slackConversationUrl(message.from) ||
    slackConversationUrl(message.to);
  const convertAction =
    onConvertToTask && mine && message.step_id ? (
      <button
        type="button"
        onClick={onConvertToTask}
        className={cn(
          "font-mono text-[10px] underline underline-offset-2",
          talk ? "text-primary-foreground/70" : "text-muted-foreground"
        )}
      >
        转为任务
      </button>
    ) : null;
  return (
    <div
      className={cn(
        talk && "msg-in",
        "flex",
        mine ? "justify-end" : "justify-start"
      )}
    >
      <div
        className={cn(
          talk ? "max-w-[85%] rounded-2xl px-3.5 py-2" : "max-w-[75%] rounded-lg px-3 py-2",
          mine
            ? cn("bg-primary text-primary-foreground", talk && "rounded-br-md")
            : cn("border bg-card", talk && "rounded-bl-md")
        )}
      >
        {!talk && (
          <div
            className={cn(
              "mb-0.5 flex items-baseline gap-2 font-mono text-[10px]",
              mine ? "text-primary-foreground/70" : "text-muted-foreground"
            )}
          >
            <span>
              {message.from} → {message.to || "?"}
            </span>
            <span>{messageTime(message.ts)}</span>
          </div>
        )}
        {message.filename && (
          <div className="mb-1 font-mono text-[10px] opacity-70">
            📎 {message.filename}
          </div>
        )}
        {mine || !talk ? (
          <div className="whitespace-pre-wrap break-words text-sm">
            {message.content}
          </div>
        ) : (
          <div className="prose prose-sm max-w-none break-words dark:prose-invert">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
              {message.content}
            </ReactMarkdown>
          </div>
        )}
        {talk ? (
          <div
            className={cn(
              "mt-0.5 flex items-center justify-end gap-2 font-mono text-[10px]",
              mine ? "text-primary-foreground/60" : "text-muted-foreground"
            )}
          >
            {convertAction}
            <span>{messageTime(message.ts)}</span>
          </div>
        ) : (
          sourceUrl && (
            <a
              href={sourceUrl}
              target="_blank"
              rel="noreferrer"
              className="mt-1 inline-flex items-center gap-1 font-mono text-[10px] opacity-70 hover:underline"
            >
              Open in Slack <ExternalLink className="h-3 w-3" />
            </a>
          )
        )}
        {!talk && convertAction && (
          <div className="mt-1 flex justify-end">{convertAction}</div>
        )}
      </div>
    </div>
  );
}

/** 乐观气泡：发送中半透明，失败后原地提供重试入口。 */
export function PendingChatBubble({
  message,
  onRetry,
  variant = "desktop",
}: {
  message: PendingMessage;
  onRetry: () => void;
  variant?: ChatBubbleVariant;
}) {
  const talk = variant === "talk";
  return (
    <div className={cn(talk && "msg-in", "flex justify-end")}>
      <div
        className={cn(
          talk
            ? "max-w-[85%] rounded-2xl rounded-br-md bg-primary px-3.5 py-2 text-primary-foreground"
            : "max-w-[75%] rounded-lg bg-primary px-3 py-2 text-primary-foreground",
          !message.failed && "opacity-70"
        )}
      >
        <div className="whitespace-pre-wrap break-words text-sm">
          {message.content}
        </div>
        {message.failed ? (
          <button
            type="button"
            className={cn(
              "mt-0.5 block w-full text-right font-mono text-[10px] underline",
              talk ? "text-red-200" : "text-primary-foreground"
            )}
            onClick={onRetry}
          >
            发送失败——点按重试
          </button>
        ) : (
          <div
            className={cn(
              "mt-0.5 text-right font-mono text-[10px]",
              talk ? "text-primary-foreground/60" : "text-primary-foreground/70"
            )}
          >
            发送中…
          </div>
        )}
      </div>
    </div>
  );
}

/** 流式回复气泡：回复生成期间的渐进展示。
 *
 * - text 为空（刚收到 replying 状态）时显示输入点动画——这就是当年被
 *   恒 0 契约字段卡死、后来删掉的「正在输入」气泡，如今由 SSE 真信号驱动。
 * - delta 到达后以**纯文本 whitespace-pre-wrap** 渲染累积全文：半截的
 *   Markdown（未闭合的 ``` 或 **）绝不进 Markdown 渲染器，避免反复
 *   重排闪烁；done 之后正式消息经 invalidate 取回，由 ChatBubble 走
 *   Markdown 渲染接替本气泡。 */
export function StreamingChatBubble({
  text,
  variant = "desktop",
}: {
  text: string;
  variant?: ChatBubbleVariant;
}) {
  const talk = variant === "talk";
  const streaming = text.length > 0;
  return (
    <div className={cn(talk && "msg-in", "flex justify-start")}>
      <div
        className={cn(
          talk
            ? "max-w-[85%] rounded-2xl rounded-bl-md border bg-card px-3.5 py-2"
            : "max-w-[75%] rounded-lg border bg-card px-3 py-2"
        )}
      >
        {streaming ? (
          <div className="whitespace-pre-wrap break-words text-sm">
            {text}
            <span
              className="ml-0.5 inline-block h-3.5 w-[2px] animate-pulse bg-muted-foreground align-text-bottom"
              aria-hidden
            />
          </div>
        ) : (
          <div
            className="flex gap-1 py-1"
            role="status"
            aria-label="正在输入回复"
          >
            <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
            <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
            <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
          </div>
        )}
      </div>
    </div>
  );
}
