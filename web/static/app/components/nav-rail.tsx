import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, LayoutGrid, Minus, Moon, PanelLeft, Plus, RefreshCw, Square, Sun, X } from "lucide-react";
import { useTheme } from "next-themes";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Link, useLocation, useNavigationType } from "react-router";
import { toast } from "sonner";

import { CredentialControl } from "~/components/credential-control";
import { NewTaskDialog } from "~/components/new-task-dialog";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import { fetchConfig, fetchIdentities, selfUpdate } from "~/lib/api";
import type { Config } from "~/lib/types";

// Trellis 左导航栏（docs/designs/ui-language.md）：新的任务（打开派发
// 对话框）+「工作区」身份列表；底部常驻操作员控件（凭据/构建）。顶排：
// 面板钮（左栏展开/收起）+ 壳内窗体 chrome（拖动/双击最大化/后退/前进，
// 对齐 Qoder 左上角导航簇）。右上角 = 主题切换 + 最小化/最大化/关闭
// 控件簇（root.tsx 挂载；浏览器里窗控三钮自动隐去、只留主题钮）。LLM
// 健康读数归右侧系统面板（导航栏不再挂气泡）。壳自身不渲染标题栏
// （capabilities/default.json 已开放 main 窗远程源 IPC）。

/** 左栏展开/收起（窄图标栏形态）。模块级外部存储：NavRail 多处渲染与
 * root.tsx 的拖动带（left 边界跟随）共享同一状态；持久化 localStorage。 */
const RAIL_KEY = "ml-rail-collapsed";
let railCollapsed = false;
try {
  railCollapsed = localStorage.getItem(RAIL_KEY) === "1";
} catch {}
const railSubs = new Set<() => void>();
export function toggleRail() {
  railCollapsed = !railCollapsed;
  try {
    localStorage.setItem(RAIL_KEY, railCollapsed ? "1" : "0");
  } catch {}
  for (const f of railSubs) f();
}
export function useRailCollapsed(): boolean {
  return useSyncExternalStore(
    (cb) => {
      railSubs.add(cb);
      return () => railSubs.delete(cb);
    },
    () => railCollapsed
  );
}

function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) return <div className="h-7 w-7" />;
  return (
    <button
      type="button"
      className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground"
      onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
      aria-label="切换主题"
      title="切换主题"
    >
      {resolvedTheme === "dark" ? (
        <Sun className="size-3.5" />
      ) : (
        <Moon className="size-3.5" />
      )}
    </button>
  );
}

/** Wait through the restart: poll /api/config until the commit changes,
 * then reload the page (which also picks up the rebuilt frontend). */
function pollForNewBuild(oldCommit: string, timeoutMs = 5 * 60 * 1000) {
  const started = Date.now();
  const tick = async () => {
    if (Date.now() - started > timeoutMs) {
      toast.error("更新超时——请在服务器上检查 mindloop web 服务的日志。");
      return;
    }
    try {
      const config = await fetchConfig();
      if (config.git_commit && config.git_commit !== oldCommit) {
        window.location.reload();
        return;
      }
    } catch {
      // server is restarting/rebuilding — keep waiting
    }
    setTimeout(tick, 3000);
  };
  setTimeout(tick, 3000);
}

/** The build stamp doubles as a meta menu: click for server details and —
 * when the server allows it — a "pull latest & restart" control. */
export function BuildMenu({ config }: { config: Config }) {
  const controlsEnabled = useControlsEnabled();
  const [open, setOpen] = useState(false);
  const [updating, setUpdating] = useState(false);

  const runUpdate = async () => {
    setUpdating(true);
    try {
      const result = await selfUpdate();
      if (!result.updated) {
        toast.info(`已是最新版本 (${result.commit})`);
        setUpdating(false);
        return;
      }
      toast.success(
        `更新中 ${result.from_commit} → ${result.to_commit}——服务重启中，恢复后页面自动刷新（约 1-2 分钟）`
      );
      pollForNewBuild(config.git_commit ?? "");
    } catch (error) {
      toast.error((error as Error).message);
      setUpdating(false);
    }
  };

  return (
    <div className="relative">
      <button
        className="inline-flex h-8 items-center font-mono text-[11px] leading-none text-muted-foreground hover:text-foreground"
        title="构建信息"
        onClick={() => setOpen(!open)}
      >
        {config.git_commit}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute bottom-8 left-0 z-50 w-72 space-y-2 rounded-md border bg-popover p-3 text-xs text-popover-foreground shadow-md">
            <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 font-mono">
              <span className="text-muted-foreground">提交</span>
              <span>
                {config.git_commit}
                {config.git_branch ? ` (${config.git_branch})` : ""}
              </span>
              <span className="text-muted-foreground">版本</span>
              <span>{config.version}</span>
              <span className="text-muted-foreground">根目录</span>
              <span className="break-all">{config.root}</span>
            </div>
            {config.self_update_enabled && controlsEnabled && (
              <Button
                variant="outline"
                size="sm"
                className="w-full"
                disabled={updating}
                onClick={runUpdate}
              >
                <RefreshCw className={`size-3 ${updating ? "animate-spin" : ""}`} />
                {updating ? "更新中…" : "拉取最新并重启"}
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function RailItem({
  to,
  label,
  active,
  icon,
  stateDot,
  live,
}: {
  to: string;
  label: string;
  active?: boolean;
  icon?: React.ReactNode;
  /** 身份项：以状态点开头的纯切换器（[状态点 + 名字]，§5）。 */
  stateDot?: boolean;
  live?: boolean;
}) {
  const collapsed = useRailCollapsed();
  return (
    <Link
      to={to}
      aria-current={active ? "page" : undefined}
      title={collapsed ? label : undefined}
      className={`group flex items-center rounded-lg py-1.5 text-[13px] ${
        collapsed ? "justify-center" : "gap-2.5 px-2.5"
      } ${
        active
          ? "bg-sidebar-accent text-sidebar-accent-foreground"
          : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground"
      }`}
    >
      {active && !collapsed && <span className="h-3.5 w-[3px] rounded-full bg-primary" />}
      {stateDot ? (
        <span className="flex w-[15px] justify-center">
          <span
            className={`h-1.5 w-1.5 rounded-full ${live ? "bg-primary" : "bg-muted-foreground/30"}`}
            title={live ? "心智运行中" : "已停止"}
          />
        </span>
      ) : (
        (icon ?? <span className="w-[15px]" aria-hidden />)
      )}
      {!collapsed && <span className={`truncate ${active ? "" : "pl-0"}`}>{label}</span>}
    </Link>
  );
}

/** 右上角控件簇：主题切换 +（壳内）最小化 / 最大化 / 关闭（关闭 = 缩入
 * 托盘，壳的 CloseRequested 处理会拦下销毁）。挂载点 = 窗口右上角
 * （root.tsx），不随导航栏；浏览器环境只渲染主题钮、窗控三钮自动隐去。 */
export function ShellControls() {
  const tauri = typeof window !== "undefined" ? (window as { __TAURI__?: any }).__TAURI__ : null;
  const w = tauri?.window?.getCurrentWindow?.();
  const btn =
    "grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground";
  return (
    <div
      className="flex items-center gap-0.5"
      onPointerDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      <ThemeToggle />
      {w && (
        <>
          <button
            className={btn}
            aria-label="最小化"
            onClick={() => w.minimize().catch(() => {})}
          >
            <Minus className="size-3.5" />
          </button>
          <button
            className={btn}
            aria-label="最大化或还原"
            onClick={() => w.toggleMaximize().catch(() => {})}
          >
            <Square className="size-3" />
          </button>
          <button
            className={`${btn} hover:bg-clay hover:text-white`}
            aria-label="关闭（缩入托盘）"
            onClick={() => w.close().catch(() => {})}
          >
            <X className="size-3.5" />
          </button>
        </>
      )}
    </div>
  );
}

/** 壳内无框窗口的拖动区：按住移动超阈值调原生 startDragging，双击切换
 * 最大化（与导航栏顶排同一套逻辑）。无导航栏的形态（talk PWA 落地页）
 * 需要在顶部铺一条，否则窗口完全拖不动。非壳环境不渲染。 */
export function ShellDragArea({ className }: { className?: string }) {
  const tauri = typeof window !== "undefined" ? (window as { __TAURI__?: any }).__TAURI__ : null;
  const dragStartRef = useRef<{ x: number; y: number } | null>(null);
  const draggedRef = useRef(false);
  if (!tauri?.window) return null;
  return (
    <div
      className={className}
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        dragStartRef.current = { x: e.clientX, y: e.clientY };
        draggedRef.current = false;
      }}
      onPointerMove={(e) => {
        const d = dragStartRef.current;
        if (!d || draggedRef.current) return;
        if (Math.hypot(e.clientX - d.x, e.clientY - d.y) < 6) return;
        draggedRef.current = true;
        dragStartRef.current = null;
        tauri?.window?.getCurrentWindow?.()?.startDragging?.().catch?.(() => {});
      }}
      onPointerUp={() => {
        dragStartRef.current = null;
        setTimeout(() => {
          draggedRef.current = false;
        }, 80);
      }}
      onDoubleClick={() =>
        tauri?.window?.getCurrentWindow?.()?.toggleMaximize?.().catch?.(() => {})
      }
    />
  );
}

/** 顶排的后退/前进钮。浏览器 history 栈没有可读索引，这里用 location.key
 * + sessionStorage 自建栈与位置：PUSH 追加、REPLACE/首帧覆盖原位、POP
 * 命中已见键。导航本身走 window.history.back()/forward()（SPA 内）。 */
function useHistoryNav(enabled: boolean) {
  const location = useLocation();
  const navType = useNavigationType();
  const firstRun = useRef(true);
  const [nav, setNav] = useState<{ stack: string[]; pos: number }>(() => {
    try {
      const raw = sessionStorage.getItem("ml-hist-v1");
      if (raw) {
        const p = JSON.parse(raw) as { stack?: unknown; pos?: unknown };
        if (Array.isArray(p.stack) && p.stack.length > 0 && typeof p.pos === "number") {
          return { stack: p.stack as string[], pos: p.pos };
        }
      }
    } catch {}
    return { stack: [location.key], pos: 0 };
  });
  useEffect(() => {
    setNav((prev) => {
      const first = firstRun.current;
      firstRun.current = false;
      let { stack, pos } = prev;
      if (stack[pos] === location.key) return prev;
      const known = stack.indexOf(location.key);
      if (known >= 0) {
        pos = known;
      } else if (navType === "REPLACE" || first) {
        stack = stack.slice();
        stack[pos] = location.key;
      } else {
        stack = stack.slice(0, pos + 1).concat(location.key);
        pos = stack.length - 1;
      }
      try {
        sessionStorage.setItem("ml-hist-v1", JSON.stringify({ stack, pos }));
      } catch {}
      return { stack, pos };
    });
  }, [location.key, navType]);
  return {
    canBack: enabled && nav.pos > 0,
    canForward: enabled && nav.pos < nav.stack.length - 1,
    back: () => window.history.back(),
    forward: () => window.history.forward(),
  };
}

export function NavRail() {
  const { pathname } = useLocation();
  const { data: config } = useQuery({
    queryKey: ["config"],
    queryFn: fetchConfig,
    staleTime: Infinity,
  });
  const { data: identities } = useQuery({
    // 与工作台/系统面板/身份档案共享同一 key：删除/新建后一处 invalidate 全栏即时刷新。
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30000,
  });
  // 活跃工作区：/i/{id}/* 与 /talk/{id} 都算。
  const activeId = decodeURIComponent(
    pathname.match(/^\/(?:i|talk)\/([^/]+)/)?.[1] ?? ""
  );

  // 顶排 = 窗体拖动区（壳内）：按住移动超阈值调原生 startDragging，
  // 双击切换最大化；拖过的松手不算点击（不误触面板钮/导航箭头）。
  const tauri = typeof window !== "undefined" ? (window as { __TAURI__?: any }).__TAURI__ ?? null : null;
  const dragStartRef = useRef<{ x: number; y: number } | null>(null);
  const draggedRef = useRef(false);
  const hist = useHistoryNav(!!tauri?.window);
  const collapsed = useRailCollapsed();
  const [taskOpen, setTaskOpen] = useState(false);

  return (
    <aside
      className={`sticky top-0 z-40 flex h-screen shrink-0 flex-col bg-sidebar py-4 transition-all duration-150 ${
        collapsed ? "w-14 px-2" : "w-56 px-2.5"
      }`}
    >
      {/* 顶排 = 窗体拖动区（壳内）：按住移动超阈值调原生 startDragging，
          双击切换最大化。窗口控制钮在窗口右上角（root.tsx 挂载）。
          排布 [面板钮][‹][›]：面板钮 = 左栏展开/收起，窄栏时只留它（居中）。 */}
      <div
        className={`mb-4 flex h-8 items-center ${collapsed ? "justify-center" : "gap-2 px-2"}`}
        onPointerDown={(e) => {
          if (e.button !== 0 || !tauri) return;
          dragStartRef.current = { x: e.clientX, y: e.clientY };
          draggedRef.current = false;
        }}
        onPointerMove={(e) => {
          const d = dragStartRef.current;
          if (!d || draggedRef.current) return;
          if (Math.hypot(e.clientX - d.x, e.clientY - d.y) < 6) return;
          draggedRef.current = true;
          dragStartRef.current = null;
          tauri?.window?.getCurrentWindow?.()?.startDragging?.().catch?.(() => {});
        }}
        onPointerUp={() => {
          dragStartRef.current = null;
          setTimeout(() => {
            draggedRef.current = false;
          }, 80);
        }}
        onDoubleClick={() =>
          tauri?.window?.getCurrentWindow?.()?.toggleMaximize?.().catch?.(() => {})
        }
      >
        <button
          type="button"
          aria-label={collapsed ? "展开侧栏" : "收起侧栏"}
          aria-expanded={!collapsed}
          title={collapsed ? "展开侧栏" : "收起侧栏"}
          onClick={() => {
            if (!draggedRef.current) toggleRail();
          }}
          className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground"
        >
          <PanelLeft className="size-4" />
        </button>
        {!collapsed && tauri?.window && (
          <div
            className="flex items-center gap-0.5"
            onPointerDown={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
          >
            <button
              type="button"
              aria-label="后退"
              title="后退"
              disabled={!hist.canBack}
              onClick={hist.back}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:pointer-events-none disabled:opacity-35"
            >
              <ChevronLeft className="size-3.5" />
            </button>
            <button
              type="button"
              aria-label="前进"
              title="前进"
              disabled={!hist.canForward}
              onClick={hist.forward}
              className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:pointer-events-none disabled:opacity-35"
            >
              <ChevronRight className="size-3.5" />
            </button>
          </div>
        )}
      </div>

      <RailItem
        to="/"
        label="工作台"
        active={pathname === "/"}
        icon={<LayoutGrid className="size-[15px]" />}
      />

      <button
        type="button"
        title={collapsed ? "新的任务" : undefined}
        onClick={() => setTaskOpen(true)}
        className={`group flex w-full items-center rounded-lg py-1.5 text-left text-[13px] text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground ${
          collapsed ? "justify-center" : "gap-2.5 px-2.5"
        }`}
      >
        <Plus className="size-[15px]" />
        {!collapsed && <span className="truncate">新的任务</span>}
      </button>

      {collapsed ? (
        <div className="h-4" />
      ) : (
        <div className="px-2.5 pb-1 pt-5 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          身份
        </div>
      )}
      <div className="flex flex-col gap-0.5">
        {(identities ?? []).map((it) => (
          <RailItem
            key={it.id}
            to={`/i/${encodeURIComponent(it.id)}`}
            label={it.name}
            active={activeId === it.id}
            stateDot
            live={it.live}
          />
        ))}
        {!collapsed && (identities?.length ?? 0) === 0 && (
          <div className="px-2.5 py-1.5 text-xs text-muted-foreground/70">尚无身份</div>
        )}
      </div>

      <div
        className={`mt-auto flex flex-col pt-3 ${
          collapsed ? "items-center gap-1" : "items-start gap-0.5"
        }`}
      >
        <div className={collapsed ? "" : "px-1.5"}>
          <CredentialControl compact={collapsed} />
        </div>
        {config?.git_commit && !collapsed && (
          <div className="px-1.5"><BuildMenu config={config} /></div>
        )}
      </div>

      <NewTaskDialog open={taskOpen} onOpenChange={setTaskOpen} />
    </aside>
  );
}
