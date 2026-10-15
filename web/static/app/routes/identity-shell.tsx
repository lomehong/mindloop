// 身份页 = 剧场（docs/designs/mockups/theater-stage.html 完整还原，
// 用户 2026-10-14 拍板）：裸入口落地「舞台」——宇宙氛围层 + 活动星
// 图铺满、卡片右上玻璃列、隔夜账卡左上、线索行左下、底部心跳行 +
// 对话条。avatar.md §6.1 砍 tab 行：顶栏 8 个平铺 tab 收敛为 ⌘K
// 命令面板（一级入口，覆盖全部路由）+ 舞台引擎室抽屉；低频引擎页
// 从 ⌘K / 需要你条 / 抽屉进入，「← 舞台」随时回客厅。工作台（home
// 路由）已重生为同行面（avatar.md §6.2）。对话单真相源（theater.md
// §3）：对话经顶栏 ⤢ 全屏浮层 + 舞台对话条。

import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useParams } from "react-router";
import { useEffect, useState } from "react";
import { parseAsString, useQueryState } from "nuqs";

import { CreatureSvg } from "~/components/pet/creature";
import { ChatOverlay } from "~/components/chat-overlay";
import { CommandPalette } from "~/components/command-palette";
import { EngineDrawer } from "~/components/engine-drawer";
import { MindlogSearch } from "~/components/mindlog-search";
import { NeedYouBar } from "~/components/need-you-bar";
import { PresenceChip, usePresence } from "~/components/stage-hud";
import { ShellDragArea, WindowButtons } from "~/components/nav-rail";
import { VitalsBand } from "~/components/vitals-band";
import { fetchIdentities } from "~/lib/api";
import { jumpTargetFor } from "~/lib/search-jump";
import { phaseFor } from "~/lib/pet-state";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

// NAV 不再渲染 tab 行（avatar.md §6.1），保留作 sectionOf/标题的
// 词表来源——路由分节语义只有这一份。标签已换生命系统轴（avatar.md
// §3.1 形态篇）：记忆→手账、感知→注意力；机制词在页内机制语境保留。
const NAV = [
  { key: "stage", label: "舞台", path: "" },
  { key: "traj", label: "轨迹", path: "/traj" },
  { key: "tasks", label: "任务", path: "/tasks" },
  { key: "memories", label: "手账", path: "/memories" },
  { key: "growth", label: "成长", path: "/growth" },
  { key: "run", label: "运行", path: "/run/thinkers" },
  { key: "review", label: "回看", path: "/review" },
  { key: "sensors", label: "注意力", path: "/sensors" },
  { key: "settings", label: "设置", path: "/settings/config" },
] as const;

type SectionKey = (typeof NAV)[number]["key"] | "run" | "chat";

function sectionOf(rest: string): SectionKey {
  if (rest.startsWith("/run")) return "run";
  if (rest.startsWith("/settings")) return "settings";
  if (rest.startsWith("/chat")) return "chat";
  if (rest.startsWith("/tasks")) return "tasks";
  if (rest.startsWith("/memories")) return "memories";
  if (rest.startsWith("/growth")) return "growth";
  if (rest.startsWith("/review")) return "review";
  if (rest.startsWith("/sensors")) return "sensors";
  // 裸入口 = 舞台（心智页默认落地，2026-10-14 拍板）；显式 /traj 系
  // 才进引擎室轨迹视图。
  if (rest === "") return "stage";
  if (rest.startsWith("/traj") || rest.startsWith("/log") || rest.startsWith("/recap") || rest.startsWith("/t/")) {
    return "traj";
  }
  return "stage";
}

const SEGS: Partial<Record<SectionKey, { label: string; path: string }[]>> = {
  traj: [
    // 泳道本体在 /traj——path:"" 曾拼出裸 /i/:id 落到舞台（审查 #8）。
    { label: "泳道", path: "/traj" },
    { label: "日志流", path: "/log" },
    { label: "分集", path: "/recap" },
  ],
  run: [
    { label: "思考者", path: "/run/thinkers" },
    { label: "作息", path: "/run/schedule" },
    { label: "体检", path: "/run/health" },
    { label: "食量", path: "/run/usage" },
  ],
  settings: [
    { label: "配置", path: "/settings/config" },
    { label: "手艺", path: "/settings/skills" },
    { label: "社交", path: "/settings/connections" },
  ],
};

function Masthead({ identityId }: { identityId: string }) {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30000,
  });
  const me = identities?.find((it) => it.id === identityId);
  const live = me?.live ?? false;
  const port = typeof window !== "undefined" ? window.location.port : "";

  return (
    <div className="flex min-h-8 items-center gap-2.5">
      <CreatureSvg size={30} phase={phaseFor(identityId)} />
      <span className="font-note text-[19px] font-semibold tracking-[0.01em]">
        {me?.name ?? identityId}
      </span>
      {live ? (
        <span className="inline-flex items-center gap-1.5 rounded-full border border-primary/30 bg-primary/12 px-2.5 py-0.5 font-mono text-[11px] text-foreground">
          <span className="size-[5px] animate-pulse rounded-full bg-primary" />
          运行中{port ? ` · :${port}` : ""}
        </span>
      ) : (
        <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted px-2.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          <span className="size-[5px] rounded-full bg-faint" />
          已停止
        </span>
      )}
    </div>
  );
}

export default function IdentityShell() {
  const { identityId = "" } = useParams();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const base = `/i/${encodeURIComponent(identityId)}`;
  const rest = pathname.slice(base.length);
  const section = sectionOf(rest);
  const seg = SEGS[section];
  const [chatOpen, setChatOpen] = useQueryState(
    "chat",
    parseAsString.withDefault("")
  );
  const { presence, setAway } = usePresence(identityId);
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
  });
  const identityName = identities?.find((it) => it.id === identityId)?.name ?? identityId;
  const sectionLabel = NAV.find((n) => n.key === section)?.label ?? section;
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  // Ctrl+K 命令面板（theater.md §16.8）：全局键盘入口。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // 命中直达：轨迹区 → 泳道 /traj（?step= 由 TimelineView 解析）；
  // 其他区（对话/记忆）→ 日志流定位。目标换算在 lib/search-jump（纯
  // 函数配测试——曾被写死 `${base}?step=` 落在舞台页静默失效）。
  const defaultJump = (hit: SearchHit) =>
    navigate(jumpTargetFor(section, base, hit.step_id));

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background">
      {/* 全局顶栏（theater-stage.html #topbar）＝无边框窗的标题栏：
          拖动层铺满整条（空隙可拖 + 双击最大化），交互元素 relative
          z-10 盖在其上正常点击，右端窗控三钮（壳外不渲染）。心智页
          自持整窗（root.tsx 不给这条分支挂壳控件），窗口管理必须
          自配——否则整窗拖不动也关不掉（2026-10-14 实测）。 */}
      <header className="relative z-40 flex h-14 shrink-0 items-center gap-3 border-b border-border/70 bg-background/90 px-5 backdrop-blur">
        <ShellDragArea className="absolute inset-0" />
        <div className="relative z-10 shrink-0">
          <Masthead identityId={identityId} />
        </div>
        {/* 砍 tab 行后（avatar.md §6.1）：非舞台视图给「← 舞台」回客厅
            + 当前分节徽标；分节内子页 chips 留在页面自己的 head 行。 */}
        {section !== "stage" && (
          <>
            <Link
              to={base}
              data-testid="back-to-stage"
              className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              ← 舞台
            </Link>
            <span className="relative z-10 whitespace-nowrap rounded-full bg-muted px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground">
              {sectionLabel}
            </span>
          </>
        )}
        {/* 体征层（avatar.md §3.4）：呼吸/心跳/食量/睡眠/痛觉——跨
            分节常驻。窄窗隐藏；pointer-events 放行给底下的拖动层
            （顶栏中段是主要「抓住移动」区域）。 */}
        <VitalsBand
          identityId={identityId}
          className="pointer-events-none relative z-10 hidden min-[1180px]:flex"
        />
        {/* 弹性空隙：pointer-events 放行给底下的拖动层——顶栏中段是
            主要的「抓住移动」区域。 */}
        <span className="pointer-events-none min-w-2 flex-1" />
        <button
          type="button"
          data-testid="palette-open"
          onClick={() => setPaletteOpen(true)}
          className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[12px] text-muted-foreground transition-colors hover:text-foreground"
        >
          命令面板 <span className="font-mono text-[10px]">Ctrl K</span>
        </button>
        {section === "stage" && (
          <button
            type="button"
            data-testid="drawer-open"
            onClick={() => setDrawerOpen(true)}
            className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
          >
            引擎室
          </button>
        )}
        <span className="relative z-10">
          <PresenceChip presence={presence} onToggle={() => setAway(!presence.away)} />
        </span>
        <button
          type="button"
          data-testid="chat-tab"
          onClick={() => setChatOpen("1")}
          className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
        >
          对话 <span aria-hidden>⤢</span>
        </button>
        <Link
          to="/"
          className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
        >
          工作台
        </Link>
        {/* 窗控三钮（壳内渲染）：最小化 / 最大化 / 关闭。 */}
        <div className="relative z-10 shrink-0">
          <WindowButtons />
        </div>
      </header>

      <div className="relative min-h-0 flex-1">
        {section === "stage" ? (
          // 舞台 = index 路由（identity-stage）：absolute inset-0 铺满
          // 顶栏以下整窗——剧场即心智页本身。引擎室抽屉仅舞台页有。
          <>
            <Outlet />
            <EngineDrawer
              identityId={identityId}
              open={drawerOpen}
              onClose={() => setDrawerOpen(false)}
            />
          </>
        ) : (
          <div
            className={
              // 对话页（书签兜底路由）同样履行 ChatPage 的高度契约：
              // 宿主定界为 flex 列、自身不滚——消息区自滚、输入坞贴底。
              section === "chat"
                ? "flex h-full flex-col overflow-hidden px-5 pb-4 sm:px-6"
                : "h-full overflow-y-auto px-5 pb-8 sm:px-6"
            }
          >
            <NeedYouBar identityId={identityId} />
            {/* engine-head（theater-stage.html .engine-head）：标题左、
                搜索右（仅轨迹——记忆页有自己的搜索记忆框） */}
            <div className="mb-[18px] flex items-baseline gap-[14px]">
              <h1 className="text-[20px] font-semibold tracking-[0.01em]">
                {sectionLabel} · {identityName}
              </h1>
              <span className="flex-1" />
              {section === "traj" && (
                <MindlogSearch
                  identityId={identityId}
                  windowStart={0}
                  onJump={defaultJump}
                />
              )}
            </div>
            {/* 子页 chips 独立成行（泳道/日志流/分集 等） */}
            {seg && (
              <div className="mb-3 flex gap-1.5">
                {seg.map((item) => {
                  const on = rest === item.path;
                  return (
                    <Link
                      key={item.path}
                      to={`${base}${item.path}`}
                      aria-current={on ? "page" : undefined}
                      className={cn(
                        "rounded-full px-2.5 py-1 font-mono text-[10px] transition-colors",
                        on
                          ? "bg-primary/10 text-primary"
                          : "border border-line text-muted-foreground hover:text-foreground"
                      )}
                    >
                      {item.label}
                    </Link>
                  );
                })}
              </div>
            )}
            <div
              className={
                section === "chat" ? "flex min-h-0 flex-1 flex-col" : undefined
              }
            >
              <Outlet />
            </div>
          </div>
        )}
      </div>
      <ChatOverlay open={chatOpen === "1"} onClose={() => setChatOpen("")} />
      <CommandPalette
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        identityId={identityId}
      />
    </div>
  );
}
