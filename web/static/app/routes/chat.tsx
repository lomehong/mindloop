import { useQuery } from "@tanstack/react-query";
import { SendHorizontal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router";

import {
  ChatBubble,
  PendingChatBubble,
  StreamingChatBubble,
} from "~/components/chat-bubble";
import { IdentityTabs } from "~/components/identity-tabs";
import { WorkingCard } from "~/components/working-card";
import {
  StartStopButtons,
  useControlsEnabled,
} from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { Textarea } from "~/components/ui/textarea";
import { useAutosizeTextarea } from "~/hooks/use-autosize-textarea";
import {
  fetchConfig,
  fetchIdentityStatus,
  fetchThinkers,
} from "~/lib/api";
import {
  CHAT_DOTS_WINDOW_MS,
  useChat,
  useNowTicker,
  useReplyStream,
} from "~/lib/use-chat";
import {
  STATUS_ACTIVE_POLL_MS,
  THINKERS_IDLE_POLL_MS,
} from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 对话" }];
}

const MY_NAME_KEY = "shellm-chat-from";

// A name the user explicitly chose here (persisted), or "" if they never have.
// When empty, we fall back to the server's CLI default (chatrc
// default_send_from) once config loads — matching what `chat send` would use.
function storedName(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(MY_NAME_KEY) || "";
}

// Whether the user has ever touched the name field. A cleared field stores ""
// — still "touched", so the chatrc default must not refill it on remount.
function hasStoredName(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(MY_NAME_KEY) !== null;
}

export default function ChatPage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();
  const [draft, setDraft] = useState("");
  const [myName, setMyName] = useState(storedName);
  const bottomRef = useRef<HTMLDivElement>(null);
  const draftRef = useAutosizeTextarea(draft);

  // Seed the from-field default from the CLI (chatrc default_send_from) unless
  // the user has ever touched the name field (tracked in localStorage, so the
  // guard survives remounts — a deliberately cleared field stays cleared).
  const { data: config } = useQuery({
    queryKey: ["config"],
    queryFn: fetchConfig,
    staleTime: Infinity,
  });
  useEffect(() => {
    if (hasStoredName()) return;
    if (config) setMyName(config.default_send_from || "you");
  }, [config]);

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_ACTIVE_POLL_MS,
  });
  const live = status?.live ?? false;

  // 桌面版取整份聊天记录（不按发送者过滤），与 PWA 共享同一条
  // 查询/发送链路：乐观气泡、失败重试、发送后快轮询全部由 useChat 提供。
  // 流式渐进气泡、实时活动进度卡同样共享（useReplyStream），两端一致。
  const {
    chat,
    messages,
    pending,
    isLoading,
    send,
    retry,
    isSending,
    lastSentAt,
  } = useChat({ identityId, myName });
  const { reply, working, activity, stepTotal } = useReplyStream({
    identityId,
    sentAt: lastSentAt,
  });
  // 进度可见期间每秒心跳：驱动 mm:ss 计时、点动画 4s 让位窗口。
  const now = useNowTicker(lastSentAt !== null || working || activity.length > 0);

  // 统一指示器：点动画只允许「发送后 4s 内」的短窗口，之后进度卡接管
  // ——两者互斥（判定公式与 talk-chat 完全一致）。
  const dotsWindow =
    lastSentAt !== null && now - lastSentAt < CHAT_DOTS_WINDOW_MS;
  const dotsActive =
    reply !== null && reply.text.length === 0 && dotsWindow;
  const showStreaming =
    reply !== null && (reply.text.length > 0 || dotsWindow);
  const waitingForReply =
    lastSentAt !== null &&
    reply === null &&
    (pending.some((p) => !p.failed) ||
      (messages.length > 0 &&
        messages[messages.length - 1]?.from === myName));
  const showCard =
    !dotsActive &&
    (working || activity.length > 0 || (waitingForReply && !dotsWindow));

  const { data: thinkerStatus } = useQuery({
    queryKey: ["thinkers", identityId],
    queryFn: () => fetchThinkers(identityId),
    refetchInterval: THINKERS_IDLE_POLL_MS,
  });
  const dispatcherRunning = thinkerStatus?.dispatcher.running ?? true;

  const identityName = chat?.identity.name ?? identityId.split("~").pop();

  const itemCount =
    messages.length +
    pending.length +
    (showStreaming ? 1 : 0) +
    (showCard ? 1 : 0);
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [itemCount, reply?.text.length, activity.length]);

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs identityId={identityId} live={live} active="chat" />
      <div className="mx-auto flex w-full max-w-3xl flex-col">

      {controlsEnabled && !dispatcherRunning && (
        <div className="mb-3 flex items-center gap-3 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          <span>
            思考者已停止——{identityName} 看不到也不会回复消息。
          </span>
          <div className="ml-auto">
            <StartStopButtons identityId={identityId} names={[]} running={false} />
          </div>
        </div>
      )}

      <div className="flex min-h-[40vh] flex-col gap-2 overflow-y-auto rounded-lg border bg-background p-4 max-h-[65vh]">
        {isLoading ? (
          <div className="flex justify-center py-10">
            <LoadingDots />
          </div>
        ) : messages.length === 0 && pending.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            还没有消息，打个招呼吧。
          </div>
        ) : (
          <>
            {messages.map((message, idx) => (
              // "you" was the hardcoded sender before default_send_from existed,
              // so that history is always ours regardless of the current name.
              <ChatBubble
                key={message.step_id ?? idx}
                message={message}
                mine={message.from === myName || message.from === "you"}
              />
            ))}
            {pending.map((message) => (
              <PendingChatBubble
                key={`pending-${message.key}`}
                message={message}
                onRetry={() => retry(message)}
              />
            ))}
            {reply !== null && (
              <StreamingChatBubble text={reply.text} variant="desktop" />
            )}
          </>
        )}
        {/* 活动卡片独立于消息分支：线程为空时（接了任务还没回话）也要可见。 */}
        <WorkingCard
          name={identityName ?? identityId}
          working={working}
          activity={activity}
          stepTotal={stepTotal}
          sentAt={lastSentAt}
          variant="desktop"
        />
        <div ref={bottomRef} />
      </div>

      {controlsEnabled && (
        <form
          className="mt-3 flex items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (!draft.trim() || isSending) return;
            send(draft);
            setDraft("");
          }}
        >
          <Input
            value={myName}
            onChange={(event) => {
              setMyName(event.target.value);
              window.localStorage.setItem(MY_NAME_KEY, event.target.value);
            }}
            title="你的名字（消息的 from 字段）"
            className="h-9 w-24 shrink-0 font-mono text-xs"
          />
          <Textarea
            ref={draftRef}
            autoFocus
            rows={1}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            enterKeyHint="send"
            onKeyDown={(event) => {
              if (
                event.key === "Enter" &&
                !event.shiftKey &&
                !event.nativeEvent.isComposing &&
                event.keyCode !== 229
              ) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
            placeholder={`给 ${identityName} 发消息…`}
            className="max-h-40 flex-1 py-2"
          />
          <Button
            type="submit"
            size="sm"
            disabled={isSending || !draft.trim()}
          >
            <SendHorizontal className="size-3.5" />
            发送
          </Button>
        </form>
      )}
      </div>
    </div>
  );
}
