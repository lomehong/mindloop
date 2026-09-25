// 聊天的共享数据层：桌面（/i/:id/chat）与 PWA（/talk/:id）共用同一条
// 查询、同一套乐观发送与失败重试逻辑。
//
// 此前两端各自维护 useQuery/useMutation，queryKey 口径不一
// （["chat", id] vs ["chat", id, name]），发消息后 invalidate 只命中
// 自己的 key，同一 SPA 会话里两份缓存互不知晓。现在统一为
// ["chat", identityId, myName]（myName 空串归一为 ""），发送后按前缀
// ["chat", identityId] 失效——双端缓存同时命中。
//
// 桌面版此前没有乐观气泡/失败重试/发送后快轮询，本层把这三者
// 作为唯一实现下放给两端。

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import {
  fetchChat,
  openReplyStream,
  sendChat,
  type BusyThinker,
  type ReplyStreamEvent,
} from "~/lib/api";
import {
  CHAT_FAST_POLL_MS,
  CHAT_FAST_POLL_WINDOW_MS,
  CHAT_IDLE_POLL_MS,
} from "~/lib/polling";

export interface PendingMessage {
  key: number;
  content: string;
  failed: boolean;
}

/** 单一 queryKey 口径：myName 为空时归一为 ""，保证形态稳定。 */
export function chatQueryKey(
  identityId: string,
  myName: string | null | undefined
): ["chat", string, string] {
  return ["chat", identityId, myName ?? ""];
}

export interface UseChatOptions {
  identityId: string;
  /** 发送者名字；参与 queryKey 与乐观消息对账。 */
  myName: string;
  /** 只取与该发送者的会话（PWA 用）；缺省取整份聊天记录（桌面用）。 */
  withName?: string | null;
  enabled?: boolean;
  tail?: number;
}

export function useChat({
  identityId,
  myName,
  withName,
  enabled = true,
  tail = 200,
}: UseChatOptions) {
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<PendingMessage[]>([]);
  const [lastSentAt, setLastSentAt] = useState<number | null>(null);
  const pendingKey = useRef(0);
  // 轮询间隔回调在 tick 时读取（非渲染期），用 effect 保持最新即可。
  const lastSentAtRef = useRef<number | null>(null);
  useEffect(() => {
    lastSentAtRef.current = lastSentAt;
  }, [lastSentAt]);

  const query = useQuery({
    queryKey: chatQueryKey(identityId, myName),
    queryFn: () => fetchChat(identityId, tail, withName || undefined),
    // 发送后的短窗口内快速轮询，其余时间慢速（节奏出处：lib/polling.ts）。
    refetchInterval: () => {
      const sent = lastSentAtRef.current;
      return sent !== null && Date.now() - sent < CHAT_FAST_POLL_WINDOW_MS
        ? CHAT_FAST_POLL_MS
        : CHAT_IDLE_POLL_MS;
    },
    enabled,
  });

  const messages = useMemo(() => query.data?.messages ?? [], [query.data]);
  const outcomes = query.data?.outcomes ?? {};

  // 收到回复（最后一条不是自己发的）即结束快速轮询窗口。
  const lastMessage = messages[messages.length - 1];
  useEffect(() => {
    if (lastMessage && lastMessage.from !== myName) setLastSentAt(null);
  }, [lastMessage, myName]);

  // 服务端 echo 出真实消息后，对应内容的乐观气泡退场（失败的保留供重试）。
  const confirmedContents = useMemo(
    () =>
      new Set(messages.filter((m) => m.from === myName).map((m) => m.content)),
    [messages, myName]
  );
  useEffect(() => {
    setPending((prev) =>
      prev.filter((p) => p.failed || !confirmedContents.has(p.content))
    );
  }, [confirmedContents]);
  const visiblePending = pending.filter(
    (p) => p.failed || !confirmedContents.has(p.content)
  );

  const sendMutation = useMutation({
    mutationFn: (content: string) => sendChat(identityId, content, myName),
    onMutate: (content: string) => {
      const key = ++pendingKey.current;
      setPending((prev) => [...prev, { key, content, failed: false }]);
      setLastSentAt(Date.now());
      return { key };
    },
    // 前缀失效：命中 ["chat", identityId, *] 全部形态，桌面/PWA 双端同步。
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chat", identityId] });
    },
    onError: (error: Error, _content, context) => {
      setPending((prev) =>
        prev.map((p) => (p.key === context?.key ? { ...p, failed: true } : p))
      );
      toast.error(error.message);
    },
  });

  /** 发送一条消息；空白内容与发送中的重复提交会被忽略。 */
  const send = (content: string) => {
    const trimmed = content.trim();
    if (!trimmed || sendMutation.isPending) return;
    sendMutation.mutate(trimmed);
  };

  /** 重试一条失败的乐观消息：先移除旧气泡，再走同一条发送链路。 */
  const retry = (message: PendingMessage) => {
    setPending((prev) => prev.filter((p) => p.key !== message.key));
    sendMutation.mutate(message.content);
  };

  return {
    chat: query.data,
    isLoading: query.isLoading,
    messages,
    outcomes,
    /** 尚未被服务端确认的乐观消息（含发送失败待重试的）。 */
    pending: visiblePending,
    /** 最近一次发送的时间戳；收到回复后复位为 null。 */
    lastSentAt,
    send,
    retry,
    isSending: sendMutation.isPending,
  };
}

// ---- 回复流式消费（SSE，第 4 层）----

export interface StreamingReply {
  /** 正在回复的那条消息（我方 pending 消息）的 step_id。 */
  replyTo: string;
  /** 到目前为止的累积回复全文（后端每次发全量，幂等整段替换）。 */
  text: string;
}

export interface ReplyStreamState {
  /** 正在生成的回复；null = 当前没有流式回复可渲染。 */
  reply: StreamingReply | null;
  /** 流式连接是否处于可用状态（收到过 status 事件且未在重连等待中）。 */
  live: boolean;
  /** 连续失败 3 次后退化为纯轮询（兼容无此端点的旧后端），不再重连。
   * 退化后回复的可见性完全依赖既有 chat 轮询（发送后 60s 快轮询窗口 +
   * 常规节奏），行为与引入流式之前一致。 */
  degraded: boolean;
  /** 心智是否在干活（working 事件；空闲 false）。 */
  working: boolean;
  /** 当前忙碌的思考者清单（working 事件附带）。 */
  busy: BusyThinker[];
  /** 最近的活动步骤（step 事件，按到达顺序，最多保留 ACTIVITY_MAX 条；
   * working=false 后延迟清空，给 WorkingCard 留出淡出时间）。 */
  activity: StepActivity[];
  /** 本次任务累计收到的 step 事件数（自最近一次发送起计数；重连不重
   * 置，发送更新即重新起算）。 */
  stepTotal: number;
}

/** WorkingCard 上展示的单条活动步骤。 */
export interface StepActivity {
  stepId: string;
  stepType: string;
  ts: string;
  excerpt: string;
  /** 客户端收到该事件的时间（Date.now()）——「最近 step <5s」的判定
   * 基准；步骤自带的 ts 是事件发生的墙钟，不适合度量到达间隔。 */
  arrivedAt: number;
}

// 活动列表上限：只关心「最近在干什么」，多了纯噪音。
export const ACTIVITY_MAX = 6;
// working=false 后保留 activity 的毫秒数——WorkingCard 播完淡出动画
// 再卸载；计时器在事件回调里安排（非 effect 体内），不触碰 lint 的
// set-state-in-effect 规则。
const ACTIVITY_LINGER_MS = 400;
// 发送后允许「点动画」独占的短窗口；超过后进度卡接管。两者互斥。
export const CHAT_DOTS_WINDOW_MS = 4000;

// 连续失败达到该次数即判定端点不可用（旧后端 404 / 持续故障），退化为
// 纯轮询。任何一次成功建立连接都会把计数清零。
const REPLY_STREAM_MAX_FAILURES = 3;
// 指数退避封顶：5s。
const REPLY_STREAM_BACKOFF_CAP_MS = 5000;

/** 每秒刷新一次的 now（毫秒）。active=false 时挂起；重新激活后先经
 * 0ms 定时器立即校准一次（不在 effect 体内同步 setState），随后按秒
 * 跳动。WorkingCard 的耗时/阶段时长与路由的点动画窗口都吃这条心跳。 */
export function useNowTicker(active: boolean, intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const initial = setTimeout(() => setNow(Date.now()), 0);
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => {
      clearTimeout(initial);
      clearInterval(id);
    };
  }, [active, intervalMs]);
  return now;
}

export function useReplyStream({
  identityId,
  enabled = true,
  retryBaseMs = 500,
  sentAt = null,
}: {
  identityId: string;
  enabled?: boolean;
  /** 重连退避基数（测试注入用）；按尝试次数指数递增，封顶 5s。 */
  retryBaseMs?: number;
  /** 最近一次发送的时间戳（useChat.lastSentAt）：耗时计时与步数起算
   * 的界定点——变化即新任务，步数/活动列表重新起算；重连不触碰。 */
  sentAt?: number | null;
}): ReplyStreamState {
  const queryClient = useQueryClient();
  const [reply, setReply] = useState<StreamingReply | null>(null);
  const [live, setLive] = useState(false);
  const [degraded, setDegraded] = useState(false);
  const [working, setWorking] = useState(false);
  const [busy, setBusy] = useState<BusyThinker[]>([]);
  const [activity, setActivity] = useState<StepActivity[]>([]);
  const [stepTotal, setStepTotal] = useState(0);
  // onEvent 闭包里要读最新 sentAt（effect 依赖不含它），经 ref 镜像；
  // 记录「当前计数归属于哪次发送」，失配即新任务。
  const sentAtRef = useRef<number | null>(null);
  const countedForRef = useRef<number | null>(null);
  useEffect(() => {
    sentAtRef.current = sentAt;
  }, [sentAt]);

  // 切换身份时给流式一次重新机会：degraded 是会话级判定，但换身份后
  // 重试的成本只有 3 次快速失败（约 1.5s），比永久卡死更可取。步数计数
  // 归属也一并作废（下一个 step 事件按新身份重新起算）。
  useEffect(() => {
    setDegraded(false);
    setReply(null);
    setWorking(false);
    setBusy([]);
    setActivity([]);
    setStepTotal(0);
    countedForRef.current = null;
  }, [identityId]);

  useEffect(() => {
    if (!enabled || degraded) return;
    const controller = new AbortController();
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let activityClearTimer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let openedThisAttempt = false;

    const scheduleReconnect = (attempt: number) => {
      setLive(false);
      const delay = Math.min(
        retryBaseMs * 2 ** attempt,
        REPLY_STREAM_BACKOFF_CAP_MS
      );
      timer = setTimeout(() => {
        if (!cancelled) void connect(attempt + 1);
      }, delay);
    };

    const onEvent = (event: ReplyStreamEvent) => {
      if (event.type === "status") {
        setLive(true);
        return;
      }
      if (event.type === "delta") {
        // text 为累积全文：直接整段替换，无需偏移管理。
        setReply({ replyTo: event.reply_to, text: event.text });
        return;
      }
      if (event.type === "working") {
        setWorking(event.working);
        setBusy(event.busy);
        if (activityClearTimer !== undefined) {
          clearTimeout(activityClearTimer);
          activityClearTimer = undefined;
        }
        if (!event.working) {
          // 收工：淡出窗口后再清空，让 WorkingCard 播完离场动画
          // （对空列表是无害的 no-op；不用闭包里的 activity 判断，
          // 避免过期读取）。
          activityClearTimer = setTimeout(() => {
            activityClearTimer = undefined;
            setActivity([]);
          }, ACTIVITY_LINGER_MS);
        }
        return;
      }
      if (event.type === "step") {
        // 步数归属界定：sentAt 与上次计数时不同 → 新的一次发送，步数与
        // 活动列表重新起算（重连时 sentAt 不变，计数连续）；相同则累加。
        // 全部发生在事件回调里，无 effect 体内 setState。
        const currentSentAt = sentAtRef.current;
        const arrivedAt = Date.now();
        if (countedForRef.current !== currentSentAt) {
          countedForRef.current = currentSentAt;
          setStepTotal(1);
          setActivity([
            {
              stepId: event.step_id,
              stepType: event.step_type,
              ts: event.ts,
              excerpt: event.excerpt,
              arrivedAt,
            },
          ]);
          return;
        }
        setStepTotal((n) => n + 1);
        // 按到达顺序保留最近 ACTIVITY_MAX 条。
        setActivity((prev) => {
          const next = [
            ...prev,
            {
              stepId: event.step_id,
              stepType: event.step_type,
              ts: event.ts,
              excerpt: event.excerpt,
              arrivedAt,
            },
          ];
          return next.length > ACTIVITY_MAX
            ? next.slice(next.length - ACTIVITY_MAX)
            : next;
        });
        return;
      }
      // done：正式消息已落轨迹。清掉流式气泡，让 chat 查询取回正式
      // 数据（invalidate 前缀命中桌面/PWA 两端的 ["chat", id, *] 形态）。
      setReply(null);
      void queryClient.invalidateQueries({ queryKey: ["chat", identityId] });
    };

    const connect = async (attempt: number) => {
      openedThisAttempt = false;
      try {
        await openReplyStream(
          identityId,
          onEvent,
          controller.signal,
          () => {
            // HTTP 建立成功即证明端点存在：失败计数清零（此后断流按
            // 瞬时故障处理，不推向退化）。
            openedThisAttempt = true;
            failures = 0;
          }
        );
        if (cancelled) return;
        // 连接被服务端正常关闭（重启/代理超时）：退避后重连。
        scheduleReconnect(attempt);
      } catch {
        if (cancelled || controller.signal.aborted) return;
        if (!openedThisAttempt) failures += 1;
        if (failures >= REPLY_STREAM_MAX_FAILURES) {
          setDegraded(true);
          setLive(false);
          return;
        }
        scheduleReconnect(attempt);
      }
    };

    void connect(0);
    return () => {
      cancelled = true;
      if (timer !== undefined) clearTimeout(timer);
      if (activityClearTimer !== undefined) clearTimeout(activityClearTimer);
      controller.abort();
    };
  }, [identityId, enabled, degraded, queryClient, retryBaseMs]);

  return {
    reply,
    live,
    degraded,
    working,
    busy,
    activity,
    stepTotal,
  };
}
