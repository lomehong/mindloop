import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useParams } from "react-router";

import { CreatureSvg } from "~/components/pet/creature";
import { MindlogSearch } from "~/components/mindlog-search";
import { fetchIdentities } from "~/lib/api";
import { phaseFor } from "~/lib/pet-state";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

// 身份页骨架（docs/designs/ui-language.md §5/§6）：单头 masthead（生物 ·
// 名字 · 状态芯片）+ 7 tab（轨迹/对话/任务/记忆/运行/感知/设置）+ 子页段；
// 页内不再自绘头部。旧 14 tab 的三视图与子页收进各自的段。

const TABS = [
  { key: "traj", label: "轨迹", path: "" },
  { key: "chat", label: "对话", path: "/chat" },
  { key: "tasks", label: "任务", path: "/tasks" },
  { key: "memories", label: "记忆", path: "/memories" },
  { key: "run", label: "运行", path: "/run/thinkers" },
  { key: "sensors", label: "感知", path: "/sensors" },
  { key: "settings", label: "设置", path: "/settings/config" },
] as const;

type SectionKey = (typeof TABS)[number]["key"];

function sectionOf(rest: string): SectionKey {
  if (rest.startsWith("/run")) return "run";
  if (rest.startsWith("/settings")) return "settings";
  if (rest.startsWith("/chat")) return "chat";
  if (rest.startsWith("/tasks")) return "tasks";
  if (rest.startsWith("/memories")) return "memories";
  if (rest.startsWith("/sensors")) return "sensors";
  return "traj";
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

/** 子页段：三个一级页签内的情境切换（轨迹三视图 / 运行四子页 / 设置三子页）。 */
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

  // 命中直达 canonical 轨迹视图：轨迹区任何子视图 → 泳道（?step= 由
  // TimelineView 解析：窗口内滚动+闪烁+开详情，窗口外走取单步弹窗）；
  // 其他区（对话/记忆）→ 日志流定位。窗口外旧步骤一律退化为取单步弹窗。
  const defaultJump = (hit: SearchHit) =>
    section === "traj"
      ? navigate(`${base}?step=${encodeURIComponent(hit.step_id)}`)
      : navigate(`${base}/log?step=${encodeURIComponent(hit.step_id)}`);

  return (
    <>
      {/* 吸附头必须不透明（/95+blur 会透字），且不做 -mt-4 负边距抵消——
         首屏视觉完全等同，钉住时头能盖满卡片顶带（滚动容器的 pt 已撤）。 */}
      <div className="sticky top-0 z-40 -mx-5 mb-4 border-b border-border/70 bg-card px-5 pt-3 sm:-mx-6 sm:px-6">
        {/* 搜索在 masthead 行（而非 tab 行）：1280 宽下 7 tab + 视图段 +
            搜索同挤一行会把 tab 压成竖排两字——搜索上移后两行各得其所。 */}
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
          {TABS.map((tab) => {
            const active = tab.key === section;
            return (
              <Link
                key={tab.key}
                to={`${base}${tab.path}`}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "relative shrink-0 whitespace-nowrap px-3 pb-2.5 pt-2 text-[13px] transition-colors",
                  active
                    ? "text-foreground after:absolute after:inset-x-3 after:-bottom-px after:h-[2px] after:rounded-t-[2px] after:bg-primary"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                {tab.label}
              </Link>
            );
          })}
          <span className="flex-1" />
          {seg && <SubSeg base={base} items={seg} rest={rest} />}
        </div>
      </div>
      <Outlet />
    </>
  );
}
