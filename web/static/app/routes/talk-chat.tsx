import { useMutation, useQuery } from "@tanstack/react-query";
import { Bot, ChevronLeft, ListTodo, SendHorizontal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import {
  ChatBubble,
  PendingChatBubble,
  StreamingChatBubble,
} from "~/components/chat-bubble";
import { PushBell } from "~/components/push-bell";
import { CredentialControl } from "~/components/credential-control";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import { LoadingDots } from "~/components/ui/loading-dots";
import { Textarea } from "~/components/ui/textarea";
import { useAutosizeTextarea } from "~/hooks/use-autosize-textarea";
import { fetchThinkers, submitTask } from "~/lib/api";
import { getPwaName, pwaSender, setLastIdentity } from "~/lib/pwa";
import type { ChatMessage } from "~/lib/types";
import {
  newClientMessageId,
  nonTaskActivity,
  useChat,
  useNowTicker,
  useReplyStream,
  CHAT_DOTS_WINDOW_MS,
} from "~/lib/use-chat";
import { WorkingCard } from "~/components/working-card";
import {
  THINKERS_AWAITING_POLL_MS,
  THINKERS_IDLE_POLL_MS,
} from "~/lib/polling";
import { cn } from "~/lib/utils";

export function meta({ params }: { params: { identityId?: string } }) {
  return [{ title: params.identityId ? `${params.identityId} · talk` : "mindloop · 对话" }];
}

// Backstop only: slow replies (a busy monolith, a long task) can
// legitimately take minutes. Declines and failures surface instantly via
// outcome stamps, not this timer.
const TYPING_TIMEOUT_MS = 180_000;

/** iOS doesn't shrink the layout viewport for the keyboard — it pans the
 * page and (in installed PWAs) often leaves it panned after dismiss,
 * stranding the chat with phantom margins. Track visualViewport (which
 * does follow the keyboard) into a CSS var, and snap the pan back when
 * the keyboard goes away. No-op on browsers without the API. */
function useKeyboardViewport() {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const root = document.documentElement;
    const update = () => {
      root.style.setProperty("--talk-height", `${vv.height}px`);
      if (vv.height >= window.innerHeight - 1) {
        window.scrollTo(0, 0);
      }
    };
    update();
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
      root.style.removeProperty("--talk-height");
    };
  }, []);
}

export default function TalkChat() {
  const { identityId = "" } = useParams();
  const navigate = useNavigate();
  const controlsEnabled = useControlsEnabled();
  useKeyboardViewport();
  const [draft, setDraft] = useState("");
  // 超时标记：记录「哪一次发送」已超时（而非布尔），发送一旦更新
  // （lastSentAt 变化）即视为未超时——派生值免去 effect 里的同步
  // setState 重置，也天然随发送刷新。
  const [expiredStamp, setExpiredStamp] = useState<number | null>(null);
  const draftRef = useAutosizeTextarea(draft);
  const awaitingRef = useRef(false);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const nearBottomRef = useRef(true);
  const didInitialScroll = useRef(false);

  const name = getPwaName();
  useEffect(() => {
    if (!name) navigate("/talk", { replace: true });
    else setLastIdentity(identityId);
  }, [name, identityId, navigate]);
  const myName = name ? pwaSender(name) : "";

  // PWA 只看自己与该身份的会话（withName 过滤）；查询、乐观气泡、
  // 失败重试与快轮询都来自共享的 useChat。回复生成期间的流式渐进气泡
  // 由 useReplyStream 提供（旧后端无此端点时自动退化为纯轮询）。
  const {
    chat,
    messages,
    outcomes,
    pending: visiblePending,
    lastSentAt,
    send,
    retry,
    isSending,
    isLoading,
    streamLiveRef,
  } = useChat({
    identityId,
    myName,
    withName: myName,
    enabled: !!myName,
  });
  // reply = 流式回复；working/activity/stepTotal = 心智干活期间的实时
  // 活动进度卡；sentAt（lastSentAt）是耗时计时与步数起算的界定点；
  // streamLiveRef 镜像 SSE 健康状态，健康期间轮询走慢节奏。
  const {
    reply,
    working,
    activity,
    stepTotal,
  } = useReplyStream({
    identityId,
    enabled: !!myName,
    sentAt: lastSentAt,
    liveRef: streamLiveRef,
  });
  // 进度可见期间每秒心跳：驱动 mm:ss 计时、点动画 4s 让位窗口。
  // 进度卡只消费非任务步骤：身份在跑任务时，任务进度不冒充聊天进度。
  const chatActivity = nonTaskActivity(activity);
  const now = useNowTicker(lastSentAt !== null || working || chatActivity.length > 0);

  // 显式委托：带幂等键提交（失败重发复用同键，服务端返回原任务而不是
  // 再落一份）；成功后跳任务页看真实状态，不在这里乐观推断。
  const submitRef = useRef<{ content: string; clientMessageId: string } | null>(null);
  const submitTaskMutation = useMutation({
    mutationFn: (input: {
      content: string;
      clientMessageId: string;
      sourceStepId?: string;
    }) =>
      submitTask(identityId, {
        content: input.content,
        fromName: myName,
        clientMessageId: input.clientMessageId,
        sourceStepId: input.sourceStepId,
      }),
    onSuccess: () => {
      submitRef.current = null;
      setDraft("");
      toast.success("已交给 Agent 执行");
      navigate(`/talk/${encodeURIComponent(identityId)}/tasks`);
    },
    onError: (error: Error) => toast.error(error.message),
  });

  /** 把当前草稿交给 Agent 执行（显式委托，不是聊天消息）。 */
  const handoffToAgent = () => {
    const trimmed = draft.trim();
    if (!trimmed || submitTaskMutation.isPending) return;
    const prev = submitRef.current;
    const clientMessageId =
      prev && prev.content === trimmed
        ? prev.clientMessageId
        : newClientMessageId();
    submitRef.current = { content: trimmed, clientMessageId };
    submitTaskMutation.mutate({ content: trimmed, clientMessageId });
  };

  /** 把已有消息转为任务，携带来源 step_id 保留关联。 */
  const convertToTask = (message: ChatMessage) => {
    if (!message.step_id || submitTaskMutation.isPending) return;
    submitTaskMutation.mutate({
      content: message.content,
      clientMessageId: newClientMessageId(),
      sourceStepId: message.step_id,
    });
  };

  // 统一指示器：点动画只允许「发送后 4s 内」的短窗口（空文本流式气泡
  // 的输入点），之后让位给进度卡——两者互斥。
  const dotsWindow =
    lastSentAt !== null && now - lastSentAt < CHAT_DOTS_WINDOW_MS;
  const dotsActive =
    reply !== null && reply.text.length === 0 && dotsWindow;

  const { data: thinkerStatus } = useQuery({
    queryKey: ["thinkers", identityId],
    queryFn: () => fetchThinkers(identityId),
    // Faster while awaiting a reply (the sleep banner tracks this feed).
    refetchInterval: () =>
      awaitingRef.current ? THINKERS_AWAITING_POLL_MS : THINKERS_IDLE_POLL_MS,
  });
  const dispatcherRunning = thinkerStatus?.dispatcher.running ?? true;

  // Waiting longer than the backstop expires the "no reply yet" note —
  // NO_REPLY (declined) and failure stamp their own outcome instead.
  // 派生：typingExpired 仅在「最近一次发送」已挂起超过 backstop 时为真；
  // 新发送（lastSentAt 变化）立即使旧 stamp 失配归 false。
  useEffect(() => {
    if (lastSentAt === null) return;
    const timer = setTimeout(
      () => setExpiredStamp(lastSentAt),
      TYPING_TIMEOUT_MS
    );
    return () => clearTimeout(timer);
  }, [lastSentAt]);
  const typingExpired = lastSentAt !== null && expiredStamp === lastSentAt;

  // The last message I sent this session, and what the mind log says
  // happened to it: "replied" / "no-reply" / "failed" / undefined (undecided).
  const lastMine = [...messages].reverse().find((m) => m.from === myName);
  const lastOutcome = lastMine?.step_id ? outcomes[lastMine.step_id] : undefined;
  const lastMessage = messages[messages.length - 1];

  const waitingForReply =
    lastSentAt !== null &&
    lastOutcome === undefined &&
    (visiblePending.some((p) => !p.failed) ||
      (lastMessage ? lastMessage.from === myName : false));

  const showDeclinedNote =
    lastSentAt !== null &&
    lastOutcome === "no-reply" &&
    (lastMessage ? lastMessage.from === myName : false);
  const showFailedNote =
    lastSentAt !== null &&
    lastOutcome === "failed" &&
    (lastMessage ? lastMessage.from === myName : false);
  const showNoReplyNote =
    waitingForReply &&
    typingExpired &&
    reply === null &&
    !showDeclinedNote &&
    !showFailedNote;

  // 等回复、正在流式接收回复、或心智在干活（活动卡片可见）时，
  // thinkers 状态流保持加速轮询。
  const awaitingOrStreaming =
    waitingForReply || reply !== null || working || activity.length > 0;

  // The thinkers poll-interval callback reads this ref at tick time (not
  // render time), so keeping it current from an effect is sufficient.
  useEffect(() => {
    awaitingRef.current = awaitingOrStreaming;
  }, [awaitingOrStreaming]);

  // 统一指示器的两半：流式气泡（有正文必显；空文本=输入点，仅 4s 窗口
  // 内）与进度卡（working/有活动/等待超窗 任一即显，点动画在场时让位）。
  const showStreaming =
    reply !== null && (reply.text.length > 0 || dotsWindow);
  const showCard =
    !dotsActive &&
    (working || chatActivity.length > 0 || (waitingForReply && !dotsWindow));

  // Follow new messages only when already reading the latest ones; while
  // a reply streams in or the working card ticks, follow their growth too
  // (same near-bottom guard).
  const itemCount =
    messages.length +
    visiblePending.length +
    (showStreaming ? 1 : 0) +
    (showCard ? 1 : 0) +
    (showDeclinedNote || showFailedNote || showNoReplyNote ? 1 : 0);
  useEffect(() => {
    if (itemCount === 0) return;
    if (!didInitialScroll.current) {
      didInitialScroll.current = true;
      bottomRef.current?.scrollIntoView({ block: "end" });
      return;
    }
    if (nearBottomRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [itemCount, reply?.text.length, chatActivity.length]);

  const identityName = chat?.identity.name ?? identityId.split("~").pop();

  return (
    <div
      className="flex h-dvh flex-col"
      style={{ height: "var(--talk-height, 100dvh)" }}
    >
      <header className="flex shrink-0 select-none flex-wrap items-center gap-1 border-b px-2 pb-2 pt-[calc(env(safe-area-inset-top)+0.5rem)]">
        <Link
          to="/talk?pick=1"
          className="flex h-9 w-9 items-center justify-center rounded-full active:bg-accent"
          aria-label="返回身份列表"
        >
          <ChevronLeft className="size-5" />
        </Link>
        <div className="flex flex-1 items-center gap-2">
          <span className="font-medium">{identityName}</span>
          <span
            className={cn(
              "inline-block h-2 w-2 rounded-full",
              chat?.live ? "bg-green-500" : "bg-muted-foreground/30"
            )}
            title={chat?.live ? "live" : "idle"}
          />
        </div>
        <Link
          to={`/talk/${encodeURIComponent(identityId)}/tasks`}
          className="flex h-9 w-9 items-center justify-center rounded-full active:bg-accent"
          aria-label="任务"
        >
          <ListTodo className="size-5" />
        </Link>
        <CredentialControl />
        {myName && <PushBell name={myName} />}
        <span className="pr-2 font-mono text-[10px] text-muted-foreground">
          {myName}
        </span>
      </header>

      {!dispatcherRunning && (
        <div className="border-b border-amber-300 bg-amber-50 px-4 py-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          {identityName} 正在睡眠（思考者已停止）——消息会等它醒来后再被看到。
        </div>
      )}

      <div
        ref={scrollerRef}
        className="flex-1 space-y-2 overflow-y-auto px-3 py-3"
        onScroll={() => {
          const el = scrollerRef.current;
          if (!el) return;
          nearBottomRef.current =
            el.scrollHeight - el.scrollTop - el.clientHeight < 120;
        }}
      >
        {isLoading ? (
          <div className="flex justify-center py-10">
            <LoadingDots />
          </div>
        ) : messages.length === 0 && visiblePending.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            还没有消息，打个招呼吧。
          </div>
        ) : (
          <>
            {messages.map((message, idx) => (
              <ChatBubble
                key={message.step_id ?? idx}
                message={message}
                mine={message.from === myName}
                variant="talk"
                onConvertToTask={() => convertToTask(message)}
              />
            ))}
            {visiblePending.map((message) => (
              <PendingChatBubble
                key={`pending-${message.key}`}
                message={message}
                onRetry={() => retry(message)}
                variant="talk"
              />
            ))}
            {showStreaming && reply !== null && (
              <StreamingChatBubble text={reply.text} variant="talk" />
            )}
            {showDeclinedNote && (
              <div className="py-2 text-center font-mono text-[10px] text-muted-foreground">
                {identityName} 已读但选择不回复
              </div>
            )}
            {showFailedNote && (
              <div className="py-2 text-center font-mono text-[10px] text-muted-foreground">
                {identityName} 尝试回复但失败了——请重试
              </div>
            )}
            {showNoReplyNote && (
              <div className="py-2 text-center font-mono text-[10px] text-muted-foreground">
                还没有回复——{identityName} 可能还在忙
              </div>
            )}
          </>
        )}
        {/* 活动进度卡独立于消息分支：线程为空时（接了任务还没回话）也要可见。 */}
        {showCard && (
          <WorkingCard
            name={identityName ?? identityId}
            working={working}
            activity={chatActivity}
            stepTotal={stepTotal}
            sentAt={lastSentAt}
            variant="talk"
          />
        )}
        <div ref={bottomRef} />
      </div>

      {controlsEnabled && (
        <form
          className="flex select-none items-end gap-2 border-t px-3 pt-2 pb-[calc(env(safe-area-inset-bottom)+0.5rem)]"
          onSubmit={(event) => {
            event.preventDefault();
            if (!draft.trim() || isSending) return;
            send(draft);
            setDraft("");
          }}
        >
          <Textarea
            ref={draftRef}
            rows={1}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            enterKeyHint="enter"
            placeholder={`给 ${identityName} 发消息…`}
            className="max-h-40 min-h-10 flex-1 rounded-3xl px-4 py-2.5"
            autoComplete="off"
          />
          <Button
            type="button"
            size="icon"
            variant="outline"
            className="h-10 w-10 shrink-0 rounded-full"
            disabled={submitTaskMutation.isPending || !draft.trim()}
            onClick={handoffToAgent}
            aria-label="交给 Agent 执行"
            title="交给 Agent 执行"
          >
            <Bot className="size-4" />
          </Button>
          <Button
            type="submit"
            size="icon"
            className="h-10 w-10 shrink-0 rounded-full"
            disabled={isSending || !draft.trim()}
            aria-label="发送"
          >
            <SendHorizontal className="size-4" />
          </Button>
        </form>
      )}
    </div>
  );
}
