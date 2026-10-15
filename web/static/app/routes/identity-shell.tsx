// 身份页 = 剧场（docs/designs/mockups/theater-stage.html 完整还原 +
// avatar 样张对齐，用户 2026-10-15 拍板视觉以样张为准）：裸入口落地
// 「舞台」——宇宙氛围层 + 舞台分区 + 底部对话条；顶栏 = 样张形态
// （← 同行面 / ← 舞台 + 身份·阶段 chip + 透视 + ⌘K + 窗控），8 tab
// 已砍（avatar.md §6.1），低频管理走 ⌘K / 引擎室抽屉 / 需要你条；
// 舞台按 X 切 X 光透视（线框叠加读数）。体征带 = 底部常驻 pill
// （vitals-band）。对话单真相源（theater.md §3）：顶栏对话按钮 +
// 舞台对话条。

import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useParams } from "react-router";
import { useEffect, useState } from "react";
import { parseAsString, useQueryState } from "nuqs";

import { ChatOverlay } from "~/components/chat-overlay";
import { CommandPalette } from "~/components/command-palette";
import { EngineDrawer } from "~/components/engine-drawer";
import { MindlogSearch } from "~/components/mindlog-search";
import { NeedYouBar } from "~/components/need-you-bar";
import { ShellDragArea, WindowButtons } from "~/components/nav-rail";
import { VitalsBand } from "~/components/vitals-band";
import { XrayOverlay } from "~/components/xray-overlay";
import { fetchIdentities, fetchIdentityEnv } from "~/lib/api";
import { jumpTargetFor } from "~/lib/search-jump";
import { parsePolicyMode } from "~/lib/growth-ledger";
import { phaseChipOf } from "~/lib/growth-dossier";
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
  { key: "diary", label: "日记", path: "/diary" },
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
  if (rest.startsWith("/diary")) return "diary";
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
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
  });
  const me = identities?.find((it) => it.id === identityId);
  const identityName = me?.name ?? identityId;
  const live = me?.live ?? false;
  const { data: env } = useQuery({
    queryKey: ["env", identityId],
    queryFn: () => fetchIdentityEnv(identityId),
    staleTime: 60_000,
  });
  // 阶段 chip = 授权档投影段名（规则公开、无百分比，avatar.md §10.1）。
  const envMode = parsePolicyMode(
    env?.env?.find((e) => e.key === "MINDLOOP_EXEC_POLICY")?.value
  ).mode;
  const phaseChip = envMode ? phaseChipOf(envMode) : null;
  const sectionLabel = NAV.find((n) => n.key === section)?.label ?? section;
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [xrayOpen, setXrayOpen] = useState(false);
  // 舞台按 X 切透视（avatar.md §6.1）；输入焦点时不算。
  useEffect(() => {
    if (section !== "stage") return;
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement | null)?.tagName ?? "";
      if (/INPUT|TEXTAREA/.test(tag)) return;
      if (e.key.toLowerCase() === "x" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        setXrayOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [section]);
  useEffect(() => {
    // 离开舞台时关透视。
    if (section !== "stage") setXrayOpen(false);
  }, [section]);

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
      {/* 全局顶栏（样张舞台形态）＝无边框窗的标题栏：拖动层铺满整条
          （空隙可拖 + 双击最大化），交互元素 relative z-10 盖在其上
          正常点击，右端窗控三钮（壳外不渲染）。 */}
      <header className="relative z-40 flex h-12 shrink-0 items-center gap-2.5 border-b border-border/70 bg-background/90 px-4 backdrop-blur">
        <ShellDragArea className="absolute inset-0" />
        <div className="relative z-10 flex shrink-0 items-center gap-2">
          {section === "stage" ? (
            <Link
              to="/"
              className="rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              ← 同行面
            </Link>
          ) : (
            <Link
              to={base}
              data-testid="back-to-stage"
              className="rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              ← 舞台
            </Link>
          )}
          <span className="flex items-center gap-1.5 rounded-full border border-line-strong px-2.5 py-1 font-mono text-[11px] text-muted-foreground">
            <span
              className={cn(
                "size-[5px] rounded-full",
                live ? "animate-pulse bg-primary" : "bg-faint"
              )}
            />
            {identityName}
            {phaseChip && (
              <span className="text-faint">· {phaseChip.split(" · ")[0]}</span>
            )}
          </span>
          {section !== "stage" && (
            <span className="rounded-full bg-muted px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground">
              {sectionLabel}
            </span>
          )}
        </div>
        <span className="pointer-events-none hidden whitespace-nowrap font-mono text-[10.5px] text-faint min-[1280px]:block">
          {section === "stage" ? "theater v2 继承 + avatar §6 零 tab" : `${sectionLabel} · ${identityName}`}
        </span>
        {/* 弹性空隙：pointer-events 放行给底下的拖动层——顶栏中段是
            主要的「抓住移动」区域。 */}
        <span className="pointer-events-none min-w-2 flex-1" />
        {section === "stage" && (
          <>
            <button
              type="button"
              data-testid="drawer-open"
              onClick={() => setDrawerOpen(true)}
              className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              引擎室
            </button>
            <button
              type="button"
              data-testid="xray-open"
              title="按 X 透视：透过仪器看同一具身体"
              onClick={() => setXrayOpen(true)}
              className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              ⓧ 透视
            </button>
          </>
        )}
        <button
          type="button"
          data-testid="palette-open"
          onClick={() => setPaletteOpen(true)}
          className="relative z-10 whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[12px] text-muted-foreground transition-colors hover:text-foreground"
        >
          ⌘K
        </button>
        {/* 窗控三钮（壳内渲染）：最小化 / 最大化 / 关闭。 */}
        <div className="relative z-10 shrink-0">
          <WindowButtons />
        </div>
      </header>

      <div className="relative min-h-0 flex-1">
        {section === "stage" ? (
          // 舞台 = index 路由（identity-stage）：absolute inset-0 铺满
          // 顶栏以下整窗——剧场即心智页本身。引擎室抽屉与 X 光仅舞台有。
          <>
            <Outlet />
            <EngineDrawer
              identityId={identityId}
              open={drawerOpen}
              onClose={() => setDrawerOpen(false)}
            />
            <XrayOverlay
              identityId={identityId}
              open={xrayOpen}
              onClose={() => setXrayOpen(false)}
            />
          </>
        ) : (
          <div
            className={
              // 对话页（书签兜底路由）同样履行 ChatPage 的高度契约：
              // 宿主定界为 flex 列、自身不滚——消息区自滚、输入坞贴底。
              // 其余引擎页 = 样张 .work 版式：max-1200 居中 + 底部留白
              // （体征 pill 常驻在底部）。
              section === "chat"
                ? "flex h-full flex-col overflow-hidden px-5 pb-16 sm:px-6"
                : "h-full overflow-y-auto"
            }
          >
            {section !== "chat" ? (
              <div className="mx-auto w-full max-w-[1200px] px-7 pb-24">
                <NeedYouBar identityId={identityId} />
                {/* engine-head（样张 .masthead）：标题左、动作右。run/
                    sensors/settings 六页自带标题与动作行——壳不再重复
                    一行（双标题是还原样张时留下的缝）。 */}
                {["traj", "memories", "review", "growth", "diary", "tasks"].includes(section) && (
                  <div className="mb-[18px] flex flex-wrap items-baseline gap-[14px] pt-5">
                    <h1 className="text-[20px] font-medium tracking-[0.01em]">
                      {sectionLabel}
                      <span className="ml-2.5 font-mono text-[12px] font-normal text-faint">
                        {identityName}
                      </span>
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
                )}
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
                <Outlet />
              </div>
            ) : (
              <div className="flex min-h-0 flex-1 flex-col">
                <Outlet />
              </div>
            )}
          </div>
        )}
      </div>
      {/* 体征带：底部常驻 pill（样张 #vitals），所有分节可见；舞台的
          居中位是对话条，pill 让到右下角；引擎页预留底部空间（pb-16）
          避免遮挡页脚内容。 */}
      <VitalsBand
        identityId={identityId}
        live={live}
        align={section === "stage" ? "right" : "center"}
      />
      <ChatOverlay open={chatOpen === "1"} onClose={() => setChatOpen("")} />
      <CommandPalette
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        identityId={identityId}
      />
    </div>
  );
}
