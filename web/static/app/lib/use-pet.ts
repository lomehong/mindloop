// 生命体的共享信号层：身份选择、SSE 实时信号、轮询兜底、播报器、
// 勿扰与两个轻动作（说话/戳醒）。
//
// 三处宿主共用这一份管线（docs/designs/pet.md）：
//   - 桌面浮窗（components/pet/pet-app.tsx，/pet）——另加窗体行为；
//   - 身份页停靠（components/pet/pet-dock.tsx，固定页面身份）；
//   - 工作台停靠（PetDock，自选身份，与浮窗共享 localStorage 同一选择）。
// 它们看到的是同一只生物：同一后端信号源、同一 mood 派生、同一播报队列。
//
// 状态机本体不在这里——派生规则全在 lib/pet-state.ts（纯函数），
// 这里只做数据获取与装配。

import { useQuery } from "@tanstack/react-query";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

import {
  authRequired,
  fetchActivity,
  fetchApprovals,
  fetchIdentities,
  fetchLlmHealth,
  fetchThinkers,
  fetchUsage,
  openReplyStream,
  sendChat,
  stepThinker,
  subscribeAuth,
  webCredentialRevision,
  type ReplyStreamEvent,
} from "~/lib/api";
import {
  approvalAnnouncement,
  breakerAnnouncement,
  budgetAnnouncements,
  createAnnouncer,
  DEFAULT_DND,
  deriveMood,
  digestAnnouncement,
  drainUnread,
  isDnd,
  MOOD_LABEL,
  offer,
  reactionForStep,
  stepAnnouncement,
  tick,
  type AnnouncementKind,
  type PetDndConfig,
  type PetMood,
  type PetReaction,
  type PetSignals,
} from "~/lib/pet-state";
import {
  PET_ACTIVITY_POLL_MS,
  PET_APPROVALS_POLL_MS,
  PET_HEALTH_POLL_MS,
  PET_SSE_RETRY_MAX_MS,
  PET_SSE_RETRY_MS,
  PET_USAGE_POLL_MS,
} from "~/lib/polling";
import type { Identity } from "~/lib/types";

const IDENTITY_KEY = "mindloop-pet-identity";
const DND_KEY = "mindloop-pet-dnd";
const STREAM_LINGER_MS = 4_000;
const REACTION_MS = 1_600;

/** 查询静默失败：宠物常驻桌面/右栏，后端暂停时逐项退化为"无读数"，
 * 不让全局 queryCache 的错误 toast 周期性刷屏。 */
function silent<T>(fn: () => Promise<T>): () => Promise<T | null> {
  return async () => {
    try {
      return await fn();
    } catch {
      return null;
    }
  };
}

function loadDnd(): PetDndConfig {
  try {
    const raw = localStorage.getItem(DND_KEY);
    if (!raw) return DEFAULT_DND;
    const parsed = JSON.parse(raw) as Partial<PetDndConfig>;
    return { ...DEFAULT_DND, ...parsed };
  } catch {
    return DEFAULT_DND;
  }
}

/** 流式回复的展示尾部：光标区永远在句尾。 */
function tailText(text: string, max = 160): string {
  const r = Array.from(text);
  return r.length <= max ? text : "…" + r.slice(-max).join("");
}

function ageText(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return "—";
  if (seconds < 60) return `${Math.round(seconds)} 秒前`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} 小时前`;
  return `${Math.round(seconds / 86400)} 天前`;
}

export interface PetBubbleView {
  kind: AnnouncementKind | "stream";
  text: string;
}

export interface UsePetOptions {
  /** 固定身份（身份页停靠 = 页面身份）：无视本地自选、不写回，也跳过
   * "自动选中第一身份"。不传 = 自选（浮窗/工作台停靠共享同一选择）。 */
  fixedIdentityId?: string;
}

export interface UsePetResult {
  // —— 身份 ——
  identities: Identity[];
  identityId: string | null;
  identityName: string;
  chooseIdentity: (id: string) => void;
  // —— 有机体 ——
  connected: boolean;
  mood: PetMood;
  busy: number;
  reaction: PetReaction | null;
  reactionSeq: number;
  /** 宿主触发的微反应（摸头等）——与 step 触发走同一条通道。 */
  fireReaction: (r: PetReaction) => void;
  bubble: PetBubbleView | null;
  unreadCount: number;
  viewUnread: () => void;
  // —— 勿扰 ——
  dnd: PetDndConfig;
  dndActive: boolean;
  toggleDnd: () => void;
  // —— 凭据 ——
  authNeeded: boolean;
  // —— 文案 ——
  /** 浮窗状态行：身份 · 心情（断连/无身份时回落到连接态文案）。 */
  statusText: string;
  /** 停靠摘要行：心情 · 最近一步（同上回落）。 */
  summaryText: string;
  // —— 动作 ——
  chatDraft: string;
  setChatDraft: (v: string) => void;
  send: () => void;
  sending: boolean;
  chatErr: string | null;
  poke: () => void;
  poking: boolean;
  pokeNote: string | null;
}

export function usePet(options: UsePetOptions = {}): UsePetResult {
  const fixed = options.fixedIdentityId;
  const pinned = fixed !== undefined;
  const credentialRevision = useSyncExternalStore(subscribeAuth, webCredentialRevision);
  const authNeeded = useSyncExternalStore(subscribeAuth, authRequired);

  // ---- 身份选择 ----
  const identitiesQ = useQuery({
    queryKey: ["pet-identities", credentialRevision],
    queryFn: silent(fetchIdentities),
    refetchInterval: PET_ACTIVITY_POLL_MS,
  });
  const identities = identitiesQ.data ?? [];
  const [selfId, setSelfId] = useState<string | null>(null);
  useEffect(() => {
    if (pinned) return;
    if (identities.length === 0) return;
    const first = identities[0];
    if (!first) return;
    let saved: string | null = null;
    try {
      saved = localStorage.getItem(IDENTITY_KEY);
    } catch {
      // 无本地存储：跟随第一个身份。
    }
    const next =
      saved && identities.some((it) => it.id === saved) ? saved : first.id;
    setSelfId(next);
    // 自动选中也要落盘：身份选择是宠物与外部（E2E 裸监听、下次启动）
    // 共享的事实，不能只有手动切换才可发现。
    try {
      if (saved !== next) localStorage.setItem(IDENTITY_KEY, next);
    } catch {
      // 无本地存储：仅本次会话生效。
    }
  }, [identities, pinned]);
  const chooseIdentity = useCallback(
    (id: string) => {
      // 停靠固定身份：切换无意义，也不污染浮窗的自选。
      if (pinned) return;
      setSelfId(id);
      try {
        localStorage.setItem(IDENTITY_KEY, id);
      } catch {
        // 无本地存储：仅本次会话生效。
      }
    },
    [pinned]
  );
  const identityId = pinned ? fixed || null : selfId;
  const identityName = identities.find((it) => it.id === identityId)?.name ?? "";

  // ---- 有机体信号（SSE） ----
  const [connected, setConnected] = useState(false);
  const [replying, setReplying] = useState(false);
  const [busy, setBusy] = useState(0);
  const [streamText, setStreamText] = useState("");
  const [streamVisible, setStreamVisible] = useState(false);
  const [reaction, setReaction] = useState<PetReaction | null>(null);
  const [reactionSeq, setReactionSeq] = useState(0);
  const [lastAlertAt, setLastAlertAt] = useState<number | null>(null);
  const [announcer, setAnnouncer] = useState(() => createAnnouncer(Date.now()));

  const replyToRef = useRef("");
  const dndRef = useRef(isDnd(loadDnd(), Date.now()));
  const hideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearHideTimer = useCallback(() => {
    if (hideTimerRef.current !== null) {
      clearTimeout(hideTimerRef.current);
      hideTimerRef.current = null;
    }
  }, []);
  const scheduleStreamHide = useCallback(() => {
    clearHideTimer();
    hideTimerRef.current = setTimeout(() => setStreamVisible(false), STREAM_LINGER_MS);
  }, [clearHideTimer]);

  useEffect(() => {
    if (!identityId) return;
    let stopped = false;
    const ctrl = new AbortController();
    let lastId = 0;
    let retry = PET_SSE_RETRY_MS;

    const handleEvent = (ev: ReplyStreamEvent) => {
      switch (ev.type) {
        case "status": {
          // 只在"新回复开始"时重置流文本——收尾的 status(replying=false,
          // reply_to="") 不能把 streamText 清掉，否则 STREAM_LINGER_MS
          // 的气泡驻留被击穿，快回复的流式表达一闪而过。
          if (ev.replying && ev.reply_to !== replyToRef.current) {
            replyToRef.current = ev.reply_to;
            setStreamText("");
          }
          setReplying(ev.replying);
          if (ev.replying) {
            setStreamVisible(true);
            clearHideTimer();
          } else {
            scheduleStreamHide();
          }
          break;
        }
        case "delta": {
          setStreamText((prev) => prev.slice(0, ev.offset) + ev.text);
          setStreamVisible(true);
          break;
        }
        case "done": {
          scheduleStreamHide();
          break;
        }
        case "working": {
          setBusy(ev.working ? ev.busy.length : 0);
          break;
        }
        case "step": {
          const now = Date.now();
          if (ev.step_type === "alert" || ev.step_type === "error") {
            setLastAlertAt(now);
          }
          const r = reactionForStep(ev.step_type);
          if (r) {
            setReaction(r);
            setReactionSeq((s) => s + 1);
          }
          const a = stepAnnouncement(ev, now);
          if (a) {
            setAnnouncer((prev) => offer(prev, a, { dnd: dndRef.current, now }));
          }
          break;
        }
      }
    };

    (async () => {
      while (!stopped) {
        try {
          await openReplyStream(identityId, handleEvent, {
            signal: ctrl.signal,
            lastEventId: lastId > 0 ? lastId : undefined,
            onOpen: () => {
              retry = PET_SSE_RETRY_MS;
              setConnected(true);
            },
            onId: (id) => {
              lastId = id;
            },
          });
        } catch {
          // 网络断/旧后端/401：统一按不可达退避重连；401 换凭据后
          // credentialRevision 变化会重建本 effect。
        }
        if (stopped) break;
        setConnected(false);
        await new Promise((r) => setTimeout(r, retry));
        retry = Math.min(retry * 2, PET_SSE_RETRY_MAX_MS);
      }
    })();

    return () => {
      stopped = true;
      ctrl.abort();
      clearHideTimer();
      setConnected(false);
      setReplying(false);
      setBusy(0);
      setStreamText("");
      setStreamVisible(false);
      replyToRef.current = "";
    };
  }, [identityId, credentialRevision, clearHideTimer, scheduleStreamHide]);

  // 微反应自动收势。
  useEffect(() => {
    if (reaction === null) return;
    const t = setTimeout(() => setReaction(null), REACTION_MS);
    return () => clearTimeout(t);
  }, [reaction, reactionSeq]);

  const fireReaction = useCallback((r: PetReaction) => {
    setReaction(r);
    setReactionSeq((s) => s + 1);
  }, []);

  // ---- 有机体信号（轮询兜底：缓变的健康/预算/审批/活性） ----
  const activityQ = useQuery({
    queryKey: ["pet-activity", identityId, credentialRevision],
    queryFn: silent(() => fetchActivity(identityId!)),
    enabled: identityId !== null,
    refetchInterval: PET_ACTIVITY_POLL_MS,
  });
  const usageQ = useQuery({
    queryKey: ["pet-usage", identityId, credentialRevision],
    queryFn: silent(() => fetchUsage(identityId!)),
    enabled: identityId !== null,
    refetchInterval: PET_USAGE_POLL_MS,
  });
  const healthQ = useQuery({
    queryKey: ["pet-llm-health", credentialRevision],
    queryFn: silent(fetchLlmHealth),
    refetchInterval: PET_HEALTH_POLL_MS,
  });
  const approvalsQ = useQuery({
    queryKey: ["pet-approvals", identityId, credentialRevision],
    queryFn: silent(() => fetchApprovals(identityId!)),
    enabled: identityId !== null,
    refetchInterval: PET_APPROVALS_POLL_MS,
  });

  // ---- 勿扰 ----
  const [dnd, setDnd] = useState<PetDndConfig>(loadDnd);
  useEffect(() => {
    dndRef.current = isDnd(dnd, Date.now());
  }, [dnd]);
  const [clock, setClock] = useState(() => Date.now());
  const dndActive = isDnd(dnd, clock);
  const toggleDnd = useCallback(() => {
    setDnd((prev) => {
      const next = { ...prev, enabled: !prev.enabled };
      try {
        localStorage.setItem(DND_KEY, JSON.stringify(next));
      } catch {
        // 无本地存储：仅本次会话生效。
      }
      return next;
    });
  }, []);

  // ---- 播报 ----
  const admission = usageQ.data?.admission ?? null;
  const budgetRatio =
    admission && admission.daily_limit > 0
      ? admission.used_today / admission.daily_limit
      : null;
  const coolingUntil = admission?.cooling_until
    ? new Date(admission.cooling_until).getTime()
    : null;
  const cooling = coolingUntil !== null && coolingUntil > clock;

  // 预算跨阈值（每日一次）与熔断边沿。
  useEffect(() => {
    if (!admission) return;
    const now = Date.now();
    setAnnouncer((prev) => {
      let next = prev;
      for (const t of budgetAnnouncements(admission)) {
        next = offer(
          next,
          { id: t.key, kind: "budget", text: t.text, ts: now },
          { dnd: dndRef.current, now, daily: true }
        );
      }
      const b = breakerAnnouncement(admission, now);
      if (b) next = offer(next, b, { dnd: dndRef.current, now });
      return next;
    });
  }, [admission]);

  // 新审批待办（id=哈希，已受理不重播）。
  const approvals = approvalsQ.data;
  useEffect(() => {
    if (!approvals?.length) return;
    const now = Date.now();
    setAnnouncer((prev) => {
      let next = prev;
      for (const ap of approvals) {
        next = offer(next, approvalAnnouncement(ap, now), { dnd: dndRef.current, now });
      }
      return next;
    });
  }, [approvals]);

  // 开场摘要：连接后稍候片刻（等首轮轮询落地），每个身份一次。
  const digestDoneRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!connected || !identityId) return;
    if (digestDoneRef.current.has(identityId)) return;
    const timer = setTimeout(() => {
      if (digestDoneRef.current.has(identityId)) return;
      digestDoneRef.current.add(identityId);
      const now = Date.now();
      const signals: PetSignals = {
        connected: true,
        replying,
        busyThinkers: busy,
        activity: activityQ.data?.state ?? null,
        lastStepAgeS: activityQ.data?.last_step_age_s ?? null,
        budgetRatio: admission && admission.daily_limit > 0 ? admission.used_today / admission.daily_limit : null,
        cooling: admission?.cooling_until ? new Date(admission.cooling_until).getTime() > now : false,
        lastAlertAgeMs: null,
      };
      setAnnouncer((prev) =>
        offer(
          prev,
          digestAnnouncement(
            {
              identityId,
              identityName: identityName || identityId,
              mood: deriveMood(signals),
              budgetText:
                signals.budgetRatio !== null
                  ? `今日 token ${Math.round(signals.budgetRatio * 100)}%`
                  : "未设预算",
              pendingApprovals: approvals?.length ?? 0,
            },
            now
          ),
          { dnd: dndRef.current, now }
        )
      );
    }, 1_500);
    return () => clearTimeout(timer);
    // 依赖收紧到触发面：数据字段故意不进依赖（摘要只在连接边沿取一次快照）。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connected, identityId]);

  // 展示驱动：500ms 一拍（播报出队/收起 + 时钟，供 TTL/冷却过期）。
  useEffect(() => {
    const t = setInterval(() => {
      const now = Date.now();
      setAnnouncer((prev) => tick(prev, now));
      setClock(now);
    }, 500);
    return () => clearInterval(t);
  }, []);

  // ---- 派生心情与展示 ----
  const signals: PetSignals = {
    connected: connected || (identityId === null && identitiesQ.data != null),
    replying,
    busyThinkers: busy,
    activity: activityQ.data?.state ?? null,
    lastStepAgeS: activityQ.data?.last_step_age_s ?? null,
    budgetRatio,
    cooling,
    lastAlertAgeMs: lastAlertAt !== null ? clock - lastAlertAt : null,
  };
  const mood = deriveMood(signals);

  const statusText = useMemo(() => {
    if (identitiesQ.data === null) return "未连接 · 等待 mindloop web";
    if (identitiesQ.data !== undefined && identities.length === 0) {
      return "尚无身份 · 先 mindloop init";
    }
    if (identityId === null) return "未连接 · 等待 mindloop web";
    return `${identityName || identityId} · ${MOOD_LABEL[mood]}`;
  }, [identitiesQ.data, identities.length, identityId, identityName, mood]);

  const summaryText = useMemo(() => {
    if (identitiesQ.data === null) return "未连接 · 等待 mindloop web";
    if (identitiesQ.data !== undefined && identities.length === 0) {
      return "尚无身份 · 先 mindloop init";
    }
    if (identityId === null) return "未连接 · 等待 mindloop web";
    return `${MOOD_LABEL[mood]} · ${ageText(activityQ.data?.last_step_age_s ?? null)}`;
  }, [identitiesQ.data, identities.length, identityId, mood, activityQ.data]);

  const bubble = useMemo(() => {
    if (streamVisible && streamText.trim() !== "") {
      return { kind: "stream" as const, text: tailText(streamText) };
    }
    if (announcer.current) {
      return { kind: announcer.current.kind, text: announcer.current.text };
    }
    return null;
  }, [streamVisible, streamText, announcer.current]);

  // ---- 菜单与动作 ----
  const [chatDraft, setChatDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [chatErr, setChatErr] = useState<string | null>(null);
  const [poking, setPoking] = useState(false);
  const [pokeNote, setPokeNote] = useState<string | null>(null);

  const send = useCallback(async () => {
    const text = chatDraft.trim();
    if (text === "" || identityId === null || sending) return;
    setSending(true);
    setChatErr(null);
    try {
      await sendChat(identityId, text, "operator");
      setChatDraft("");
    } catch (e) {
      setChatErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSending(false);
    }
  }, [chatDraft, identityId, sending]);

  const poke = useCallback(async () => {
    if (identityId === null || poking) return;
    setPoking(true);
    setPokeNote(null);
    try {
      const st = await fetchThinkers(identityId);
      const name =
        st.thinkers.find((t) => t.name === "monolith" && t.state !== "disabled")
          ?.name ??
        st.thinkers.find((t) => t.state !== "disabled")?.name ??
        null;
      if (name === null) {
        setPokeNote("没有可戳醒的思考者——先在仪表盘启动心智");
        return;
      }
      await stepThinker(identityId, name);
      setPokeNote(`已戳醒 ${name}（消耗自发档预算）`);
    } catch (e) {
      setPokeNote(e instanceof Error ? e.message : String(e));
    } finally {
      setPoking(false);
    }
  }, [identityId, poking]);

  const viewUnread = useCallback(() => {
    setAnnouncer((prev) => drainUnread(prev));
  }, []);

  return {
    identities,
    identityId,
    identityName,
    chooseIdentity,
    connected,
    mood,
    busy,
    reaction,
    reactionSeq,
    fireReaction,
    bubble,
    unreadCount: announcer.unread.length,
    viewUnread,
    dnd,
    dndActive,
    toggleDnd,
    authNeeded,
    statusText,
    summaryText,
    chatDraft,
    setChatDraft,
    send,
    sending,
    chatErr,
    poke,
    poking,
    pokeNote,
  };
}
