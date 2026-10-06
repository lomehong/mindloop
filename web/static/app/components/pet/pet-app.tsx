import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { usePet } from "~/lib/use-pet";

import { Creature, PetStatusLine } from "./creature";
import { PetBubble, PetMenu } from "./pet-menu";

// 桌面宠物装配层：信号层在 lib/use-pet（与仪表盘停靠共用同一只生物），
// 这里补上窗体行为（拖动/穿透/位置记忆/隐藏/摸头）并装配页面形态。
// 浏览器直接开 /pet 时是同一套渲染，窗体行为自然退化（no-op）。

const POS_KEY = "mindloop-pet-pos";

export function PetApp() {
  // 根元素引用：目光追随的 CSS 变量挂在它上面
  const rootRef = useRef<HTMLDivElement>(null);
  // ---- 壳环境（Tauri）与信号层 ----
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

  const {
    identities,
    identityId,
    chooseIdentity,
    connected,
    mood,
    busy,
    reaction,
    reactionSeq,
    fireReaction,
    bubble,
    unreadCount,
    viewUnread,
    dnd,
    dndActive,
    toggleDnd,
    authNeeded,
    statusText,
    chatDraft,
    setChatDraft,
    send,
    sending,
    chatErr,
    poke,
    poking,
    pokeNote,
  } = usePet();
  const [menuOpen, setMenuOpen] = useState(false);
  // 拖动结束/隐藏前把窗体位置写回 localStorage（见位置记忆段）。
  const savePosRef = useRef<(() => void) | null>(null);

  // ---- 宠物页形态：透明背景、无滚动 ----
  useEffect(() => {
    document.documentElement.classList.add("pet-mode");
    return () => document.documentElement.classList.remove("pet-mode");
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
    if (tauri?.core?.invoke) {
      // 壳内「隐藏」= 关闭桌面宠物开关（托盘勾选随之复位，可再开启）——
      // 只隐自己的窗会被壳的形态对账立刻显示回来。旧壳没有该命令，
      // 退回原地隐窗（旧壳也无形态对账，行为与从前一致）。
      tauri.core.invoke("pet_hide").catch(() => win?.hide?.());
    } else {
      win?.hide?.();
    }
  }, [tauri, win]);

  // 凭据缺失时自动展开菜单引导补录。
  useEffect(() => {
    if (authNeeded) setMenuOpen(true);
  }, [authNeeded]);

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
      className="pet"
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
            unreadCount={unreadCount}
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
                fireReaction("happy");
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
          {unreadCount > 0 && (
            <span
              className="pet-unread-dot"
              data-testid="pet-unread-dot"
              title={`勿扰中攒了 ${unreadCount} 条未读`}
            />
          )}
          <Creature
            mood={mood}
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
