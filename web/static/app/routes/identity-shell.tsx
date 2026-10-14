// 身份页 = 剧场（docs/designs/mockups/theater-stage.html 完整还原，
// 用户 2026-10-14 拍板）：裸入口落地「舞台」——宇宙氛围层 + 活动星
// 图铺满、卡片右上玻璃列、隔夜账卡左上、线索行左下、底部心跳行 +
// 对话条；顶栏导航切引擎室全屏视图（轨迹/任务/记忆/运行/感知/设置/
// 回看）。工作台（home 路由）保留年轮原样——它的布局不在剧场射程。
// 对话单真相源（theater.md §3）：对话经顶栏 ⤢ 全屏浮层 + 舞台对话条。

import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useParams } from "react-router";
import { useCallback, useEffect, useState } from "react";
import { parseAsString, useQueryState } from "nuqs";

import { CreatureSvg } from "~/components/pet/creature";
import { ChatOverlay } from "~/components/chat-overlay";
import { MindlogSearch } from "~/components/mindlog-search";
import { NeedYouBar } from "~/components/need-you-bar";
import IdentityStage from "~/routes/identity-stage";
import { PresenceChip, usePresence } from "~/components/stage-hud";
import { fetchIdentities } from "~/lib/api";
import { phaseFor } from "~/lib/pet-state";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

const NAV = [
  { key: "stage", label: "舞台", path: "" },
  { key: "traj", label: "轨迹", path: "/traj" },
  { key: "tasks", label: "任务", path: "/tasks" },
  { key: "memories", label: "记忆", path: "/memories" },
  { key: "review", label: "回看", path: "/review" },
  { key: "sensors", label: "感知", path: "/sensors" },
  { key: "settings", label: "设置", path: "/settings/config" },
] as const;

type SectionKey = (typeof NAV)[number]["key"] | "run" | "chat";

function sectionOf(rest: string): SectionKey {
  if (rest.startsWith("/run")) return "run";
  if (rest.startsWith("/settings")) return "settings";
  if (rest.startsWith("/chat")) return "chat";
  if (rest.startsWith("/tasks")) return "tasks";
  if (rest.startsWith("/memories")) return "memories";
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
    { label: "泳道", path: "" },
    { label: "日志流", path: "/log" },
    { label: "分集", path: "/recap" },
  ],
  run: [
    { label: "思考者", path: "/run/thinkers" },
    { label: "日程", path: "/run/schedule" },
    { label: "健康", path: "/run/health" },
    { label: "用量", path: "/run/usage" },
  ],
  settings: [
    { label: "配置", path: "/settings/config" },
    { label: "技能", path: "/settings/skills" },
    { label: "连接", path: "/settings/connections" },
  ],
};

const SEARCH_SECTIONS: SectionKey[] = ["traj", "chat", "memories"];

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

/** 子页段：一级区内的情境切换（轨迹三视图 / 运行四子页 / 设置三子页）。 */
function SubSeg({
  base,
  items,
  rest,
}: {
  base: string;
  items: { label: string; path: string }[];
  rest: string;
}) {
  return (
    <div className="flex shrink-0 gap-0.5 rounded-lg bg-muted p-0.5">
      {items.map((item) => {
        const on = rest === item.path;
        return (
          <Link
            key={item.path}
            to={`${base}${item.path}`}
            aria-current={on ? "page" : undefined}
            className={cn(
              "whitespace-nowrap rounded-md px-2.5 py-1 text-xs transition-colors",
              on
                ? "bg-card text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {item.label}
          </Link>
        );
      })}
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

  // 命中直达：轨迹区子视图 → 泳道（?step= 由 TimelineView 解析）；
  // 其他区（对话/记忆）→ 日志流定位。窗口外旧步骤退化为取单步弹窗。
  const defaultJump = (hit: SearchHit) =>
    section === "traj"
      ? navigate(`${base}?step=${encodeURIComponent(hit.step_id)}`)
      : navigate(`${base}/log?step=${encodeURIComponent(hit.step_id)}`);

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background">
      {/* 全局顶栏（theater-stage.html #topbar）：身份 + 分区导航 + 在场 */}
      <header className="relative z-40 flex h-14 shrink-0 items-center gap-3 border-b border-border/70 bg-background/90 px-5 backdrop-blur">
        <Masthead identityId={identityId} />
        <nav className="flex gap-0.5">
          {NAV.map((item) => {
            const active = item.key === section;
            return (
              <Link
                key={item.key}
                to={`${base}${item.path}`}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "whitespace-nowrap rounded-lg px-3 py-1.5 text-[13px] transition-colors",
                  active
                    ? "bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
                )}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
        <span className="flex-1" />
        <PresenceChip presence={presence} onToggle={() => setAway(!presence.away)} />
        <button
          type="button"
          data-testid="chat-tab"
          onClick={() => setChatOpen("1")}
          className="whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
        >
          对话 <span aria-hidden>⤢</span>
        </button>
        <Link
          to="/"
          className="whitespace-nowrap rounded-lg border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
        >
          工作台
        </Link>
      </header>

      <div className="relative min-h-0 flex-1">
        {section === "stage" ? (
          // 舞台 = index 路由（identity-stage）：absolute inset-0 铺满
          // 顶栏以下整窗——剧场即心智页本身。
          <Outlet />
        ) : (
          <div className="h-full overflow-y-auto px-5 pb-8 sm:px-6">
            <NeedYouBar identityId={identityId} />
            <div className="sticky top-0 z-40 -mx-5 mb-4 border-b border-border/70 bg-card px-5 pt-3 sm:-mx-6 sm:px-6">
              <div className="flex items-center gap-3 pt-3">
                <Masthead identityId={identityId} />
                {SEARCH_SECTIONS.includes(section) && (
                  <MindlogSearch
                    identityId={identityId}
                    windowStart={0}
                    onJump={defaultJump}
                  />
                )}
              </div>
              <div className="mt-3 flex items-center gap-0.5 border-b border-border/70">
                {NAV.filter((n) => n.key !== "stage").map((item) => {
                  const active = item.key === section;
                  return (
                    <Link
                      key={item.key}
                      to={`${base}${item.path}`}
                      aria-current={active ? "page" : undefined}
                      className={cn(
                        "relative shrink-0 whitespace-nowrap px-3 pb-2.5 pt-2 text-[13px] transition-colors",
                        active
                          ? "text-foreground after:absolute after:inset-x-3 after:-bottom-px after:h-[2px] after:rounded-t-[2px] after:bg-primary"
                          : "text-muted-foreground hover:text-foreground"
                      )}
                    >
                      {item.label}
                    </Link>
                  );
                })}
                <span className="flex-1" />
                {seg && <SubSeg base={base} items={seg} rest={rest} />}
              </div>
            </div>
            <Outlet />
          </div>
        )}
      </div>
      <ChatOverlay open={chatOpen === "1"} onClose={() => setChatOpen("")} />
    </div>
  );
}
