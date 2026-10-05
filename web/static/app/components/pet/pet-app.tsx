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
  type PetDndConfig,
  type PetReaction,
  type PetSignals,
} from "~/lib/pet-state";
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
  PET_ACTIVITY_POLL_MS,
  PET_APPROVALS_POLL_MS,
  PET_HEALTH_POLL_MS,
  PET_SSE_RETRY_MAX_MS,
  PET_SSE_RETRY_MS,
  PET_USAGE_POLL_MS,
} from "~/lib/polling";

import { Creature, PetStatusLine } from "./creature";
import { PetBubble, PetMenu } from "./pet-menu";

// 桌面宠物装配层：把 SSE/轮询的有机体信号接进 pet-state 的纯状态机，
// 并在 Tauri 壳里补上窗体行为（拖动/穿透/位置记忆/隐藏）。
// 浏览器直接开 /pet 时是同一套渲染，窗体行为自然退化（no-op）。

const IDENTITY_KEY = "mindloop-pet-identity";
const DND_KEY = "mindloop-pet-dnd";
const POS_KEY = "mindloop-pet-pos";
const STREAM_LINGER_MS = 4_000;
const REACTION_MS = 1_600;

/** 查询静默失败：宠物常驻桌面，后端暂停时逐项退化为"无读数"，不让
 * 全局 queryCache 的错误 toast 每 30s 刷一次屏。 */
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

export function PetApp() {
  // 根元素引用：目光追随的 CSS 变量挂在它上面
  const rootRef = useRef<HTMLDivElement>(null);
  // ---- 壳环境（Tauri）与凭据 ----
  const tauri = useMemo(
    () => (typeof window !== "undefined" ? (window as { __TAURI__?: any }).__TAURI__ ?? null : null),
    []
  );
  // ---- 目光追随：瞳孔朝光标方向微微偏移（rAF 节流，限幅 ±2px） ----
  useEffect(() => {
    let raf = 0;
    const onMove = (e: PointerEvent) => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        const el = rootRef.current;
        if (!el) return;
        const r = el.getBoundingClientRect();
        const dx = Math.max(-2, Math.min(2, (e.clientX - (r.left + r.width / 2)) / 140));
        const dy = Math.max(-1.5, Math.min(1.5, (e.clientY - (r.top + r.height * 0.55)) / 140));
        el.style.setProperty("--pet-look-x", dx.toFixed(2));
        el.style.setProperty("--pet-look-y", dy.toFixed(2));
      });
    };
    window.addEventListener("pointermove", onMove);
    return () => {
      window.removeEventListener("pointermove", onMove);
      if (raf) cancelAnimationFrame(raf);
    };
  }, []);
  const credentialRevision = useSyncExternalStore(subscribeAuth, webCredentialRevision);
  const authNeeded = useSyncExternalStore(subscribeAuth, authRequired);

  // ---- 宠物页形态：透明背景、无滚动 ----
  useEffect(() => {
    document.documentElement.classList.add("pet-mode");
    return () => document.documentElement.classList.remove("pet-mode");
  }, []);

  // ---- 身份选择 ----
  const identitiesQ = useQuery({
    queryKey: ["pet-identities", credentialRevision],
    queryFn: silent(fetchIdentities),
    refetchInterval: PET_ACTIVITY_POLL_MS,
  });
  const identities = identitiesQ.data ?? [];
  const [identityId, setIdentityId] = useState<string | null>(null);
  useEffect(() => {
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
    setIdentityId(next);
    // 自动选中也要落盘：身份选择是宠物与外部（E2E 裸监听、下次启动）
    // 共享的事实，不能只有手动切换才可发现。
    try {
      if (saved !== next) localStorage.setItem(IDENTITY_KEY, next);
    } catch {
      // 无本地存储：仅本次会话生效。
    }
  }, [identities]);
  const chooseIdentity = useCallback((id: string) => {
    setIdentityId(id);
    try {
      localStorage.setItem(IDENTITY_KEY, id);
    } catch {
      // 无本地存储：仅本次会话生效。
    }
  }, []);
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
  const [menuOpen, setMenuOpen] = useState(false);
  const savePosRef = useRef<(() => void) | null>(null);

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

  // 凭据缺失时自动展开菜单引导补录。
  useEffect(() => {
    if (authNeeded) setMenuOpen(true);
  }, [authNeeded]);

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

  // getCurrentWindow() 每次调用都返回新对象——必须记忆化，否则下面
  // 两个窗体 effect 的依赖每次渲染都变，"位置恢复"会周期性把用户
  // 拖好的位置灌回旧坐标（拖完弹回去的元凶）。
  const win = useMemo(() => tauri ? tauri.window?.getCurrentWindow?.() ?? null : null, [tauri]);

  // IPC 权限体检：壳窗体的窗体控制全走 Tauri IPC，能力（capabilities）
  // 缺失时它们会被静默拒绝——拖动/穿透/隐藏全体失灵还不报错（v2 对
  // 远程源默认全拒）。这里显式探一次，把结果暴露到 data-ipc 和菜单，
  // 失配时用户看得见原因。null = 浏览器形态，无壳可探。
  const [ipcOk, setIpcOk] = useState<boolean | null>(null);
  useEffect(() => {
    if (!win) return;
    let alive = true;
    win
      .isVisible?.()
      .then(() => {
        if (alive) setIpcOk(true);
      })
      .catch(() => {
        if (alive) setIpcOk(false);
      });
    return () => {
      alive = false;
    };
  }, [win]);

  // ---- 拖动：抓着光点/状态行移动 ----
  // 窗体的空白区是点击穿透的（光标轮询 setIgnoreCursorEvents），mousedown
  // 根本到不了 drag-region，所以"拖空白处"不可行；桌面宠物的惯例就是
  // 拖本体。移动超阈值才调原生 startDragging（OS 移动循环，跟手），
  // 原样松手算单击（开菜单）——draggedRef 给 onClick 消抖。
  const dragStartRef = useRef<{ x: number; y: number } | null>(null);
  const draggedRef = useRef(false);
  // 摸头：按住 700ms 不动不拖 = 摸头（开心眯眼 + 小心心），松手不弹菜单
  const petHoldRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pettedRef = useRef(false);
  const toggleMenu = useCallback(() => {
    if (draggedRef.current || pettedRef.current) {
      draggedRef.current = false;
      pettedRef.current = false;
      return;
    }
    setMenuOpen((v) => !v);
  }, []);
  const clearPetHold = useCallback(() => {
    if (petHoldRef.current !== null) {
      clearTimeout(petHoldRef.current);
      petHoldRef.current = null;
    }
  }, []);

  const openDashboard = useCallback(() => {
    setMenuOpen(false);
    if (tauri?.core?.invoke) {
      tauri.core.invoke("pet_open_dashboard").catch(() => {});
    } else {
      window.open("/", "_blank");
    }
  }, [tauri]);

  const hidePet = useCallback(() => {
    setMenuOpen(false);
    savePosRef.current?.();
    win?.hide?.();
  }, [win]);

  // Escape 收菜单。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // ---- Tauri 窗体行为 ----

  // 位置记忆：恢复——只在窗体生命周期内跑一次；拖动中不回灌。
  const restoredRef = useRef(false);
  useEffect(() => {
    if (!win || restoredRef.current) return;
    restoredRef.current = true;
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(POS_KEY);
    } catch {
      return;
    }
    if (!raw) return;
    try {
      const pos = JSON.parse(raw) as { x?: number; y?: number };
      if (typeof pos.x === "number" && typeof pos.y === "number") {
        const P = tauri?.dpi?.PhysicalPosition;
        win.setPosition(P ? new P(pos.x, pos.y) : { x: pos.x, y: pos.y }).catch(() => {});
      }
    } catch {
      // 坏数据：用默认位置。
    }
  }, [win, tauri]);

  // 位置记忆：拖动结束（pointerup）与隐藏前落盘。
  useEffect(() => {
    if (!win) return;
    const save = () => {
      win
        .outerPosition()
        .then((p: { x: number; y: number }) => {
          try {
            localStorage.setItem(POS_KEY, JSON.stringify({ x: p.x, y: p.y }));
          } catch {
            // 无本地存储：不记忆位置。
          }
        })
        .catch(() => {});
    };
    savePosRef.current = save;
    window.addEventListener("pointerup", save);
    return () => {
      savePosRef.current = null;
      window.removeEventListener("pointerup", save);
    };
  }, [win]);

  // 位置记忆：原生拖动（startDragging 的 OS 移动循环）不回发
  // pointerup，用窗口 moved 事件去抖落盘。
  useEffect(() => {
    if (!win || typeof win.onMoved !== "function") return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let unlisten: (() => void) | null = null;
    win
      .onMoved(() => {
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => savePosRef.current?.(), 400);
      })
      .then((f: () => void) => {
        unlisten = f;
      })
      .catch(() => {});
    return () => {
      unlisten?.();
      if (timer) clearTimeout(timer);
    };
  }, [win]);

  // 点击穿透：光标不在实心元素上时放行桌面点击。穿透状态下 DOM 收不到
  // 鼠标事件，所以轮询光标位置而不是监听 pointermove。
  useEffect(() => {
    if (
      !win ||
      typeof win.cursorPosition !== "function" ||
      typeof win.setIgnoreCursorEvents !== "function"
    ) {
      return;
    }
    let stopped = false;
    let ignoring = false;
    (async () => {
      while (!stopped) {
        try {
          const cur = await win.cursorPosition();
          const dpr = window.devicePixelRatio || 1;
          let solid = false;
          if (cur && Number.isFinite(cur.x) && Number.isFinite(cur.y)) {
            for (const el of Array.from(document.querySelectorAll("[data-pet-solid]"))) {
              const r = el.getBoundingClientRect();
              if (
                cur.x >= r.left * dpr &&
                cur.x <= r.right * dpr &&
                cur.y >= r.top * dpr &&
                cur.y <= r.bottom * dpr
              ) {
                solid = true;
                break;
              }
            }
          }
          if (solid !== !ignoring) {
            ignoring = !solid;
            await win.setIgnoreCursorEvents(ignoring);
          }
        } catch {
          // cursorPosition 不可用：保持当前模式（可从托盘恢复）。
        }
        await new Promise((r) => setTimeout(r, 150));
      }
    })();
    return () => {
      stopped = true;
    };
  }, [win]);

  return (
    <div
      ref={rootRef}
      className={`pet pet-mood-${mood}`}
      data-testid="pet-root"
      data-mood={mood}
      // 裸 SSE 订阅状态（区别于 mood 的 offline——后者在身份未选出时
      // 会被"身份列表可读"顶替；E2E 发消息前要等的是这一位）。
      data-sse={connected ? "on" : "off"}
      data-ipc={ipcOk === null ? "n/a" : ipcOk ? "ok" : "broken"}
      data-tauri-drag-region
      onMouseDown={(e) => {
        // 只在空白处按下才收菜单——菜单/光点内部按下时若收起，
        // click 事件会落在被卸载的节点上，按钮全部失灵。
        const target = e.target as Element | null;
        if (target?.closest?.("[data-pet-solid]")) return;
        setMenuOpen(false);
      }}
    >
      <div className="pet-stage" data-tauri-drag-region>
        {bubble && <PetBubble text={bubble.text} kind={bubble.kind} />}
        {menuOpen && (
          <PetMenu
            identities={identities}
            identityId={identityId}
            onChooseIdentity={chooseIdentity}
            dnd={dnd}
            dndActive={dndActive}
            onToggleDnd={toggleDnd}
            unreadCount={announcer.unread.length}
            onViewUnread={viewUnread}
            chatDraft={chatDraft}
            onChatDraftChange={setChatDraft}
            onSend={send}
            sending={sending}
            chatErr={chatErr}
            onPoke={poke}
            poking={poking}
            pokeNote={pokeNote}
            onOpenDashboard={openDashboard}
            onHide={hidePet}
            authNeeded={authNeeded}
            note={
              ipcOk === false
                ? "桌面壳权限缺失（capabilities）——拖动/穿透/隐藏不可用，请覆盖安装新版桌面壳"
                : null
            }
          />
        )}
        <div
          className="pet-body"
          data-pet-solid
          onPointerDown={(e) => {
            if (e.button !== 0) return;
            dragStartRef.current = { x: e.clientX, y: e.clientY };
            draggedRef.current = false;
            pettedRef.current = false;
            clearPetHold();
            petHoldRef.current = setTimeout(() => {
              petHoldRef.current = null;
              if (!draggedRef.current) {
                pettedRef.current = true;
                setReaction("happy");
                setReactionSeq((s) => s + 1);
              }
            }, 700);
          }}
          onPointerMove={(e) => {
            const d = dragStartRef.current;
            if (!d) return;
            if (Math.hypot(e.clientX - d.x, e.clientY - d.y) < 6) return;
            clearPetHold();
            if (draggedRef.current) return;
            draggedRef.current = true;
            dragStartRef.current = null;
            win?.startDragging?.().catch(() => {});
          }}
          onPointerUp={() => {
            dragStartRef.current = null;
            clearPetHold();
            if (draggedRef.current) savePosRef.current?.();
          }}
        >
          {announcer.unread.length > 0 && (
            <span
              className="pet-unread-dot"
              data-testid="pet-unread-dot"
              title={`勿扰中攒了 ${announcer.unread.length} 条未读`}
            />
          )}
          <Creature
            busy={busy}
            speaking={mood === "speaking"}
            reaction={reaction}
            reactionSeq={reactionSeq}
            onToggleMenu={toggleMenu}
            onOpenDashboard={openDashboard}
          />
          <PetStatusLine text={statusText} mood={mood} />
        </div>
      </div>
    </div>
  );
}
