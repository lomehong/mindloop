// 聊天气泡的单一实现：桌面（/i/:id/chat）与 PWA（/talk/:id）共用。
// variant 只承载两端有意的视觉/信息密度差异——桌面=单列时间线：心智
// 消息是衬线卡（写声部），操作员消息是素条+左规（说声部）；PWA 对收到
// 的消息渲染 Markdown、时间靠右、圆角更大。mine 判定、时间格式化、乐观
// 气泡与失败重试只有这一份实现。

import { ExternalLink } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link } from "react-router";

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
  fromMind,
  onConvertToTask,
  taskHref,
  domId,
}: {
  message: ChatMessage;
  mine: boolean;
  variant?: ChatBubbleVariant;
  /** 桌面单列布局用：作者是否心智本体（衬线卡）；缺省按 !mine 推断。
   * PWA 忽略此属性（气泡左右仍按 mine）。 */
  fromMind?: boolean;
  /** 提供时，自己的消息（有 step_id）显示「转为任务」入口——以该步
   * 骤为 source_step_id 提交显式委托，保留来源关联。 */
  onConvertToTask?: () => void;
  /** 任务书消息（kind=task）的「查看任务」入口目标；缺省只显示
   * 「任务」徽章（无任务面的宿主）。 */
  taskHref?: string;
  /** 目录/深链的定位锚点（对话回合大纲用）。 */
  domId?: string;
}) {
  const talk = variant === "talk";
  const isTask = message.kind === "task";
  const sourceUrl =
    slackSourceUrl(message.source_url) ||
    slackConversationUrl(message.from) ||
    slackConversationUrl(message.to);
  // 任务书消息本身就是任务，不再提供「转为任务」（重复委托）。
  const convertAction =
    onConvertToTask && mine && message.step_id && !isTask ? (
      <button
        type="button"
        onClick={onConvertToTask}
        className={cn(
          "font-mono text-[10px] underline underline-offset-2",
          talk ? "text-primary-foreground/70" : "text-muted-foreground hover:text-foreground"
        )}
      >
        转为任务
      </button>
    ) : null;

  if (!talk) {
    const mind = fromMind ?? !mine;
    return (
      <div className="mb-5" id={domId}>
        <div className="mb-1.5 flex items-baseline gap-2 font-mono text-[10.5px] text-faint">
          <span className="text-muted-foreground">{message.from}</span>
          <span>{messageTime(message.ts)}</span>
          {message.filename && <span className="truncate">📎 {message.filename}</span>}
        </div>
        {mind ? (
          <div className="max-w-[46rem] rounded-xl border border-line bg-secondary px-4 py-3">
            <div className="prose prose-sm max-w-none break-words font-note text-[14.5px] leading-[1.8] dark:prose-invert">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>
                {message.content}
              </ReactMarkdown>
            </div>
          </div>
        ) : (
          <div className="whitespace-pre-wrap break-words border-l-2 border-line-strong pl-3.5 text-sm text-muted-foreground">
            {message.content}
          </div>
        )}
        {sourceUrl && (
          <a
            href={sourceUrl}
            target="_blank"
            rel="noreferrer"
            className="mt-1.5 inline-flex items-center gap-1 font-mono text-[10px] text-muted-foreground hover:underline"
          >
            在 Slack 中打开 <ExternalLink className="h-3 w-3" />
          </a>
        )}
        {isTask && (
          <div className="mt-1.5 flex items-center gap-2 font-mono text-[10.5px] text-muted-foreground">
            <span className="rounded-full border border-line-strong px-2 py-px">
              任务
            </span>
            {taskHref && (
              <Link
                to={taskHref}
                className="text-primary underline-offset-4 hover:underline"
              >
                查看任务 →
              </Link>
            )}
          </div>
        )}
        {convertAction && <div className="mt-1.5 flex justify-end">{convertAction}</div>}
      </div>
    );
  }

  return (
    <div className={cn("msg-in flex", mine ? "justify-end" : "justify-start")} id={domId}>
      <div
        className={cn(
          "max-w-[85%] rounded-2xl px-3.5 py-2",
          mine
            ? "rounded-br-md bg-primary text-primary-foreground"
            : "rounded-bl-md border bg-card"
        )}
      >
        {message.filename && (
          <div className="mb-1 font-mono text-[10px] opacity-70">
            📎 {message.filename}
          </div>
        )}
        {mine ? (
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
        {isTask && (
          <div className="mt-1 flex items-center gap-2 font-mono text-[10px] opacity-80">
            <span className="rounded-full border border-current/40 px-1.5 py-px">
              任务
            </span>
            {taskHref && (
              <Link to={taskHref} className="underline underline-offset-2">
                查看任务 →
              </Link>
            )}
          </div>
        )}
        <div
          className={cn(
            "mt-0.5 flex items-center justify-end gap-2 font-mono text-[10px]",
            mine ? "text-primary-foreground/60" : "text-muted-foreground"
          )}
        >
          {convertAction}
          <span>{messageTime(message.ts)}</span>
        </div>
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
  if (!talk) {
    return (
      <div className="mb-5">
        <div
          className={cn(
            "whitespace-pre-wrap break-words border-l-2 border-line-strong pl-3.5 text-sm text-muted-foreground",
            !message.failed && "opacity-60"
          )}
        >
          {message.content}
        </div>
        {message.failed ? (
          <button
            type="button"
            className="mt-1 block font-mono text-[10px] text-clay underline"
            onClick={onRetry}
          >
            发送失败——点按重试
          </button>
        ) : (
          <div className="mt-1 font-mono text-[10px] text-faint">发送中…</div>
        )}
      </div>
    );
  }
  return (
    <div className="msg-in flex justify-end">
      <div
        className={cn(
          "max-w-[85%] rounded-2xl rounded-br-md px-3.5 py-2",
          message.failed
            ? "border border-clay/40 bg-clay/10 text-foreground"
            : "bg-primary text-primary-foreground",
          !message.failed && "opacity-70"
        )}
      >
        <div className="whitespace-pre-wrap break-words text-sm">
          {message.content}
        </div>
        {message.failed ? (
          <button
            type="button"
            className="mt-0.5 block w-full text-right font-mono text-[10px] text-clay underline"
            onClick={onRetry}
          >
            发送失败——点按重试
          </button>
        ) : (
          <div className="mt-0.5 text-right font-mono text-[10px] text-primary-foreground/60">
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
  const typingDots = (
    <div className="flex gap-1 py-1" role="status" aria-label="正在输入回复">
      <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
      <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
      <span className="typing-dot h-1.5 w-1.5 rounded-full bg-muted-foreground" />
    </div>
  );
  if (!talk) {
    return (
      <div className="mb-5">
        <div className="max-w-[46rem] rounded-xl border border-line bg-secondary px-4 py-3 font-note text-[14.5px] leading-[1.8]">
          {streaming ? (
            <div className="whitespace-pre-wrap break-words">
              {text}
              <span
                className="ml-0.5 inline-block h-3.5 w-[2px] animate-pulse bg-muted-foreground align-text-bottom"
                aria-hidden
              />
            </div>
          ) : (
            typingDots
          )}
        </div>
      </div>
    );
  }
  return (
    <div className="msg-in flex justify-start">
      <div className="max-w-[85%] rounded-2xl rounded-bl-md border bg-card px-3.5 py-2">
        {streaming ? (
          <div className="whitespace-pre-wrap break-words text-sm">
            {text}
            <span
              className="ml-0.5 inline-block h-3.5 w-[2px] animate-pulse bg-muted-foreground align-text-bottom"
              aria-hidden
            />
          </div>
        ) : (
          typingDots
        )}
      </div>
    </div>
  );
}
