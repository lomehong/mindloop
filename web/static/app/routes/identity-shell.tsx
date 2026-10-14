import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation, useNavigate, useParams } from "react-router";
import { useCallback, useEffect, useState } from "react";

import { CreatureSvg } from "~/components/pet/creature";
import { ChatOverlay } from "~/components/chat-overlay";
import { MindlogSearch } from "~/components/mindlog-search";
import { NeedYouBar } from "~/components/need-you-bar";
import {
  SceneBadge,
  SceneStage,
  useSceneStream,
  type LeavingCard,
} from "~/components/scene-layer";
import { fetchIdentities } from "~/lib/api";
import { phaseFor } from "~/lib/pet-state";
import type { SceneStreamEvent, SceneSurface } from "~/lib/scene";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

// 身份页骨架（docs/designs/ui-language.md §5/§6 + theater.md §3 对话
// 单真相源）：单头 masthead + tab（对话不再是 tab——收敛为头部「对话」
// 按钮的全屏展开态与舞台对话条，/chat 路由保留作书签兜底）+ 子页段。

const TABS = [
  { key: "traj", label: "轨迹", path: "" },
  { key: "tasks", label: "任务", path: "/tasks" },
  { key: "memories", label: "记忆", path: "/memories" },
  { key: "run", label: "运行", path: "/run/thinkers" },
  { key: "sensors", label: "感知", path: "/sensors" },
  { key: "settings", label: "设置", path: "/settings/config" },
] as const;

type SectionKey = (typeof TABS)[number]["key"] | "chat";

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
    { label: "回看", path: "/review" },
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

  // —— 仪表盘舞台（scene.md §7 舞台形态 + theater.md v2）——场景有
  // 卡时接管整块内容区（tab 内容收起），撤卡回归工作区；操作员
  // 「返回工作区」后，inform/ambient 新卡只给呼吸角标不抢屏，
  // confront 始终直接接管（它的定义就是必须停下决策）。身份切换 /
  // 场景清空即复位。对话展开态（§3）舞台与工作区共用一个浮层。
  //
  // op 级事件（theater.md §11/§16.7）：upsert → E1 推卡前兆脉冲；
  // remove → ttl 沉淀为单行摘要、其余 reason 播退场动效后由定时器
  // 清理（scene.css 的退场 keyframes 260ms 内播完）。
  const [pulseFx, setPulseFx] = useState<
    { t0: number; quadrant: "center" | "right" } | null
  >(null);
  const [leavingCards, setLeavingCards] = useState<LeavingCard[]>([]);
  const [sedimented, setSedimented] = useState<
    Array<{ surface: SceneSurface }>
  >([]);
  const onSceneEventHandler = useCallback((ev: SceneStreamEvent) => {
    if (ev.type === "upsert") {
      setPulseFx({
        t0: ev.t0,
        quadrant:
          ev.surface.intent === "confront" || ev.surface.focus
            ? "center"
            : "right",
      });
      return;
    }
    if (ev.reason === "ttl") {
      setSedimented((prev) => [...prev.slice(-5), { surface: ev.surface }]);
      return;
    }
    setLeavingCards((prev) => [
      ...prev.slice(-4),
      { surface: ev.surface, reason: ev.reason, key: ev.t0 },
    ]);
    setTimeout(
      () => setLeavingCards((prev) => prev.filter((l) => l.key !== ev.t0)),
      600
    );
  }, []);
  const scene = useSceneStream(identityId, onSceneEventHandler);
  const [stageDismissed, setStageDismissed] = useState(false);
  const [chatOpen, setChatOpen] = useState(false);
  useEffect(() => {
    setStageDismissed(false);
  }, [identityId]);
  useEffect(() => {
    if (scene.surfaces.length === 0) setStageDismissed(false);
  }, [scene.surfaces.length]);
  const confront = scene.surfaces.find(
    (s) => s.intent === "confront" || s.focus === true
  );
  const stageActive =
    scene.surfaces.length > 0 && (!!confront || !stageDismissed);

  const stageBlock = stageActive ? (
    // absolute inset-0：铺满滚动容器可视区（root.tsx 的 relative 锚
    // + 内边距一并覆盖），与圆角内容卡严丝合缝——舞台高度不靠猜。
    <div className="absolute inset-0 z-20">
      <SceneStage
        surfaces={scene.surfaces}
        identityId={identityId}
        pulse={pulseFx}
        leaving={leavingCards}
        sedimented={sedimented}
        onBack={() => setStageDismissed(true)}
        onOpenChat={() => setChatOpen(true)}
      />
    </div>
  ) : null;

  // 命中直达 canonical 轨迹视图：轨迹区任何子视图 → 泳道（?step= 由
  // TimelineView 解析：窗口内滚动+闪烁+开详情，窗口外走取单步弹窗）；
  // 其他区（对话/记忆）→ 日志流定位。窗口外旧步骤一律退化为取单步弹窗。
  const defaultJump = (hit: SearchHit) =>
    section === "traj"
      ? navigate(`${base}?step=${encodeURIComponent(hit.step_id)}`)
      : navigate(`${base}/log?step=${encodeURIComponent(hit.step_id)}`);

  // 记忆式落地（theater.md §5）：/i/:id 裸入口 → 上次停留的视图；
  // 显式段不重定向，首次（无存留）恒轨迹。
  useEffect(() => {
    if (rest !== "" || !identityId) return;
    const saved = window.localStorage.getItem(
      `mindloop-last-section:${identityId}`
    );
    if (saved && saved !== "traj" && saved !== section) {
      navigate(`${base}${saved === "chat" ? "/chat" : `/${saved}`}`, {
        replace: true,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identityId]);
  useEffect(() => {
    if (!identityId) return;
    window.localStorage.setItem(
      `mindloop-last-section:${identityId}`,
      section
    );
  }, [section, identityId]);

  return (
    <>
      {stageBlock}
      {!stageActive && <NeedYouBar identityId={identityId} />}
      {/* 吸附头必须不透明（/95+blur 会透字），且不做 -mt-4 负边距抵消——
         首屏视觉完全等同，钉住时头能盖满卡片顶带（滚动容器的 pt 已撤）。 */}
      <div className={cn("sticky top-0 z-40 -mx-5 mb-4 border-b border-border/70 bg-card px-5 pt-3 sm:-mx-6 sm:px-6", stageActive && "hidden")}>
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
          {/* 对话单真相源（theater.md §3）：tab 收敛为按钮的全屏展开态 */}
          <button
            type="button"
            data-testid="chat-tab"
            onClick={() => setChatOpen(true)}
            className={cn(
              "ml-1 shrink-0 whitespace-nowrap rounded-md px-3 pb-2.5 pt-2 text-[13px] transition-colors",
              section === "chat"
                ? "text-foreground"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            对话 <span aria-hidden>⤢</span>
          </button>
        </div>
      </div>
      {/* 舞台接管态：工作区内容卸载（查询随之停），头部隐藏保持布局 */}
      {!stageActive && <Outlet />}
      {stageDismissed && scene.surfaces.length > 0 && (
        <SceneBadge
          count={scene.surfaces.length}
          onOpen={() => setStageDismissed(false)}
        />
      )}
      <ChatOverlay open={chatOpen} onClose={() => setChatOpen(false)} />
    </>
  );
}
