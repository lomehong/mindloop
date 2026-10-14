/** 场景卡片层（scene.md §7 渲染端 + theater.md v2 舞台分区制）：
 * 订阅场景流，把服务端折叠好的场景状态投影成「剧场舞台」——
 * 宇宙氛围层铺底，confront 卡遮罩独占中央（全局层，跨内容呈现），
 * inform/ambient 卡在右列（至多 3 张整卡 + 溢出折叠 chip），底部
 * 对话条是对话单真相源（§3），心跳行是累积读数（§7）。渲染端只有
 * 投影与过渡，没有业务决策：卡片上的按钮只上报 intent（POST
 * /scene/intent）。kind 注册表：不认识的 kind 降级为 text 占位
 * （title=kind），不得崩溃。 */
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { openSceneStream, postSceneIntent, type SceneExitReason, type SceneStreamEvent, type SceneSurface, type SceneState } from "~/lib/scene";
import { fetchApprovals } from "~/lib/api";
import { fmtWaiting, loadDecision, partitionCards, saveDecision, variationCompactIds } from "~/lib/theater";
import { cn } from "~/lib/utils";
import { TalkStrip } from "~/components/talk-strip";
import { SceneCosmos } from "~/components/scene-cosmos";
import {
  OvernightDigest,
  PresenceChip,
  WorkHeartbeat,
  usePresence,
  useStageTelemetry,
} from "~/components/stage-hud";
import type { StoredDecision } from "~/lib/theater";

const EMPTY_SCENE: SceneState = { rev: 0, surfaces: [] };

/** useSceneStream 维护一条场景流的生命周期：连接 → 状态折叠 → 断线
 * 重连（2s 起步指数退避，封顶 15s）。identityId 变化即重建（切身份
 * = 换一条流）。onEvent 可选：op 级事件在状态更新前发出（E1 推卡
 * 脉冲 / 按 reason 退场动画的派生源，theater.md §11/§16.7）。
 * visibility 无关：卡片面在任何 tab 都在场。 */
export function useSceneStream(
  identityId: string,
  onEvent?: (ev: SceneStreamEvent) => void
): SceneState {
  const [state, setState] = useState<SceneState>(EMPTY_SCENE);
  const eventRef = useRef(onEvent);
  useEffect(() => {
    eventRef.current = onEvent;
  }, [onEvent]);
  useEffect(() => {
    if (!identityId) return;
    const controller = new AbortController();
    let stopped = false;
    let backoff = 2000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const connect = () => {
      if (stopped) return;
      openSceneStream(
        identityId,
        setState,
        { signal: controller.signal, onEvent: (ev) => eventRef.current?.(ev) }
      )
        .then(() => {
          // 服务端正常关闭（如身份被删）：按断线处理走重连。
          if (!stopped) timer = setTimeout(connect, backoff);
        })
        .catch(() => {
          if (stopped) return;
          backoff = Math.min(backoff * 2, 15000);
          timer = setTimeout(connect, backoff);
        });
    };
    connect();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      controller.abort();
    };
  }, [identityId]);
  return state;
}

/** surface 的展示标题（变奏归组与降级摘要都用它）：data.title 优先，
 * label 次之，摘要截断兜底。 */
function titleOf(s: SceneSurface): string {
  if (typeof s.data?.title === "string" && s.data.title) return s.data.title;
  if (typeof s.data?.label === "string" && s.data.label) return s.data.label;
  return (s.summary ?? s.kind).slice(0, 40);
}

/** progress 卡是否已完成（E2 收工叙事的触发信号——真实数据派生）。 */
function progressDone(s: SceneSurface): boolean {
  if (s.kind !== "progress") return false;
  if (s.data?.status === "done") return true;
  const value = typeof s.data?.value === "number" ? s.data.value : 0;
  const max = typeof s.data?.max === "number" && s.data.max > 0 ? s.data.max : 100;
  return !s.data?.indeterminate && value >= max;
}

/** SurfaceCard 是 kind 注册表：每种 kind 一个渲染函数。data 是纯
 * 展示数据——组件不 eval、不注入 DOM，React 默认转义即达标。 */
function SurfaceCard({
  surface,
  identityId,
  compact = false,
  onDecision,
}: {
  surface: SceneSurface;
  identityId: string;
  compact?: boolean;
  onDecision?: (dir: "allow" | "deny") => void;
}) {
  const { kind, data, summary } = surface;
  if (kind === "choice") {
    return <ChoiceCard surface={surface} identityId={identityId} onDecision={onDecision} />;
  }
  if (compact) {
    return (
      <div className="flex items-baseline gap-2 overflow-hidden">
        <span className="shrink-0 text-[12px] font-medium text-foreground">{titleOf(surface)}</span>
        <span className="min-w-0 flex-1 truncate text-[11.5px] text-muted-foreground">
          {typeof data?.body === "string" && data.body ? data.body.replace(/\n/g, " ") : summary}
        </span>
      </div>
    );
  }
  if (kind === "text") {
    return (
      <div>
        {typeof data?.title === "string" && data.title && (
          <div className="mb-1 text-[13px] font-medium text-foreground">{data.title}</div>
        )}
        <div className="whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">
          {typeof data?.body === "string" && data.body ? data.body : summary}
        </div>
        {typeof data?.footnote === "string" && data.footnote && (
          <div className="mt-1 text-[11px] text-muted-foreground">{data.footnote}</div>
        )}
      </div>
    );
  }
  if (kind === "progress") {
    const max = typeof data?.max === "number" && data.max > 0 ? data.max : 100;
    const value = typeof data?.value === "number" ? Math.min(Math.max(data.value, 0), max) : 0;
    const indeterminate = data?.indeterminate === true;
    const done = data?.status === "done" || (!indeterminate && value >= max);
    const errored = data?.status === "error";
    return (
      <div>
        <div className="mb-1 flex items-baseline justify-between gap-2">
          <span className="text-[13px] font-medium text-foreground">
            {typeof data?.label === "string" && data.label ? data.label : summary}
          </span>
          <span className="text-[11px] tabular-nums text-muted-foreground">
            {indeterminate ? "…" : `${Math.round((value / max) * 100)}%`}
          </span>
        </div>
        <div className="h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-[width] duration-500",
              errored ? "bg-destructive" : done ? "bg-primary" : "bg-primary/70",
              indeterminate && "w-1/3 animate-pulse"
            )}
            style={indeterminate ? undefined : { width: `${(value / max) * 100}%` }}
          />
        </div>
        {typeof data?.note === "string" && data.note && (
          <div className="mt-1 text-[11px] text-muted-foreground">{data.note}</div>
        )}
      </div>
    );
  }
  // 未知 kind：text 占位降级（title=kind），协议要求不得崩溃。
  return (
    <div>
      <div className="mb-1 text-[13px] font-medium text-foreground">{kind}</div>
      {summary && <div className="text-[13px] text-foreground/90">{summary}</div>}
    </div>
  );
}

/** ChoiceCard 是 confront 决策卡（scene.md §6 审批桌面化 + theater.md
 * §16.3/§16.6 审批增补）：选项点击即 intent 回流（POST /scene/intent），
 * 审批类 surface 由后端换回 hash 走 policy.Decide。 theatre 增补：
 * - 依据行：按 surface id（approve-<hash8>）匹配审批队列的真实载荷
 *   （risks/等待时长）——投影端只呈现真实数据，不猜风险；
 * - 记住偏好：按脚本首行归组存上次决定，同类二次审批给「按上次
 *   决定」快捷键。
 * 点击后本地锁定；卡片消失由服务端撤卡驱动，前端不猜测结果。 */
function ChoiceCard({
  surface,
  identityId,
  onDecision,
}: {
  surface: SceneSurface;
  identityId: string;
  onDecision?: (dir: "allow" | "deny") => void;
}) {
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState("");
  const options = Array.isArray(surface.data?.options)
    ? (surface.data?.options as Array<{ value: string; label: string; tone?: string }>)
    : [];
  const promptText =
    typeof surface.data?.prompt === "string" && surface.data?.prompt
      ? (surface.data?.prompt as string)
      : surface.summary;
  const context = surface.data?.context as Record<string, unknown> | undefined;
  const script = typeof context?.script === "string" ? context.script : "";
  const workDir = typeof context?.work_dir === "string" ? context.work_dir : "";

  // 依据行数据：审批队列按 hash 前缀匹配（surface id = approve-<hash8>）。
  const { data: approvals } = useQuery({
    queryKey: ["choice-approvals", identityId],
    queryFn: () => fetchApprovals(identityId),
    refetchInterval: 5000,
  });
  const hashPrefix = surface.id.startsWith("approve-") ? surface.id.slice("approve-".length) : "";
  const matched = approvals?.find((a) => a.hash.startsWith(hashPrefix)) ?? null;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const waitingFor = matched ? fmtWaiting(now - Date.parse(matched.created)) : null;

  // 记住偏好：同命令族上次的真实决定（localStorage，per identity）。
  // 只读初始值——决定落盘后卡片由服务端撤卡驱动消失，无需本地翻转。
  const [lastDecision] = useState<StoredDecision | null>(() =>
    script ? loadDecision(window.localStorage, identityId, script) : null
  );

  const submit = async (value: string) => {
    if (pending) return;
    setPending(value);
    const dir: StoredDecision = value === "deny" ? "deny" : "approve";
    try {
      await postSceneIntent(identityId, {
        surface: surface.id,
        name: "select",
        data: { value },
      });
      if (script && (value === "approve" || value === "deny")) {
        saveDecision(window.localStorage, identityId, script, dir);
      }
      onDecision?.(dir === "deny" ? "deny" : "allow");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPending(null);
    }
  };

  return (
    <div>
      <div className="mb-2 text-[13px] font-medium text-foreground">{promptText}</div>
      {(matched || script) && (
        <div className="mb-1 text-[11px] leading-relaxed text-muted-foreground">
          依据：
          {matched?.risks?.length ? (
            <span className="text-foreground/80">{matched.risks.join("、")}</span>
          ) : (
            <span className="text-foreground/80">审批策略组命中</span>
          )}
          {waitingFor && <> · 已等 {waitingFor}</>}
          {workDir && <> · 目录 {workDir}</>}
        </div>
      )}
      {script && (
        <pre className="mb-3 max-h-40 overflow-auto rounded-md bg-muted p-2 text-[11px] leading-relaxed text-foreground/80">
          {script}
        </pre>
      )}
      <div className="flex gap-2">
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            data-testid={`scene-select-${o.value}`}
            disabled={pending !== null}
            onClick={() => submit(o.value)}
            className={cn(
              "flex-1 rounded-lg border px-3 py-1.5 text-[13px] transition-colors disabled:opacity-50",
              o.tone === "danger"
                ? "border-destructive/40 text-destructive hover:bg-destructive/10"
                : "border-primary/40 text-primary hover:bg-primary/10",
              pending === o.value && "bg-primary/10"
            )}
          >
            {pending === o.value ? "已提交…" : o.label}
          </button>
        ))}
        {lastDecision &&
          options.some((o) => o.value === lastDecision) &&
          (lastDecision === "approve" || lastDecision === "deny") && (
            <button
              type="button"
              data-testid="scene-select-repeat"
              disabled={pending !== null}
              onClick={() => submit(lastDecision)}
              title="上次同类操作你做了这个决定——记住偏好（theater.md §16.3）"
              className="rounded-lg border border-primary/25 bg-primary/8 px-3 py-1.5 text-[13px] text-primary transition-colors hover:bg-primary/15 disabled:opacity-50"
            >
              按上次决定（{lastDecision === "approve" ? "允许" : "拒绝"}）
            </button>
          )}
      </div>
      {pending !== null && (
        <div className="mt-2 text-[11px] text-muted-foreground">
          已提交，等待心智继续执行；卡片会在决策生效后自动消失
        </div>
      )}
      {error && <div className="mt-2 text-[11px] text-destructive">{error}</div>}
    </div>
  );
}

/** 舞台宇宙的特效信号（E1 推卡前兆 / E2 收工 / E4 决定 / 在听）——
 * SceneStage 从场景流与对话条派生，传给宇宙层驱动一次性动画。 */
export interface StageFx {
  pulse: { t0: number; quadrant: "center" | "right" } | null;
  decision: { dir: "allow" | "deny"; t0: number } | null;
  completion: { t0: number } | null;
  listening: boolean;
}

/** 退场卡（theater.md §16.7）：remove 已发生，内容保留到退场动画
 * 播完（按 reason 选动效，scene.css keyframes），随后由宿主清理。 */
export interface LeavingCard {
  surface: SceneSurface;
  reason: SceneExitReason;
  key: number;
}

/** ThreadsRow 是舞台左下的线索行（theater.md 线索层）：只读投影，
 * 进行中/搁置线索一眼可见；数据来自 /threads（心智进程 threads.json），
 * web 侧不开写口。 */
function ThreadsRow({ identityId }: { identityId: string }) {
  const { data: threads } = useQuery({
    queryKey: ["threads", identityId],
    queryFn: async () => {
      const res = await fetch(
        `/api/identities/${encodeURIComponent(identityId)}/threads`
      );
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return (await res.json()) as Array<{
        id: string;
        title: string;
        note?: string;
        status: string;
      }>;
    },
    refetchInterval: 15_000,
  });
  // 数组守卫：端点异常/桩测试返回非数组时不渲染（不是崩溃）。
  const open = (Array.isArray(threads) ? threads : [])
    .filter((t) => t.status !== "done")
    .slice(0, 4);
  if (open.length === 0) return null;
  return (
    <div className="absolute bottom-6 left-6 z-20 w-[300px] rounded-xl border border-line bg-card/85 p-3 backdrop-blur-[8px]">
      <div className="mb-2 font-mono text-[10px] tracking-[0.08em] text-faint">
        线索
      </div>
      {open.map((t) => (
        <div
          key={t.id}
          className="flex items-baseline gap-2 rounded-md px-1 py-1 text-[12px] hover:bg-muted/40"
        >
          <span
            className={cn(
              "shrink-0 rounded-full px-1.5 font-mono text-[10px]",
              t.status === "parked"
                ? "bg-resin/10 text-resin"
                : "bg-primary/10 text-primary"
            )}
          >
            {t.status === "parked" ? "搁置" : "进行中"}
          </span>
          <span className="min-w-0 truncate text-foreground">{t.title}</span>
          {t.note && (
            <span className="min-w-0 truncate text-[11px] text-faint">
              {t.note}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

/** SceneStage 是仪表盘的舞台形态（theater.md v2 分区制）：宇宙铺
 * 底；conform 卡遮罩独占中央；inform/ambient 右列至多 3 张整卡 +
 * 溢出折叠 chip + 变奏/沉淀单行卡；底部心跳行 + 对话条；顶栏在场
 * 芯片与对话展开。 absolute inset-0 挂 identity-shell——铺满滚动
 * 容器可视区，高度不靠猜。 */
export function SceneStage({
  surfaces,
  identityId,
  pulse,
  leaving,
  sedimented,
  onExpand,
}: {
  surfaces: SceneSurface[];
  identityId: string;
  /** E1 推卡前兆（theater.md §11）：upsert 事件派生的核心球脉冲。 */
  pulse: StageFx["pulse"];
  /** 退场中的卡（按 reason 播退场动效，宿主定时清理）。 */
  leaving: LeavingCard[];
  /** TTL 沉淀卡（§16.2）：降为单行摘要条，不占右列 3 张配额。 */
  sedimented: Array<{ surface: SceneSurface }>;
  /** 对话条展开（对话单真相源，theater.md §3）。 */
  onExpand: () => void;
}) {
  const tele = useStageTelemetry(identityId);
  // 隔夜账卡的判定基准（§6）：离开时刻取本机可见性账本。
  const { presence, lastVisibleMs: seen } = usePresence(identityId);
  const lastSeenMs = presence.away
    ? Math.min(seen, presence.sinceMs || seen)
    : seen;
  const [expanded, setExpanded] = useState(false);
  const [listening, setListening] = useState(false);
  const [decisionFx, setDecisionFx] = useState<StageFx["decision"]>(null);
  const [completionFx, setCompletionFx] = useState<StageFx["completion"]>(null);

  const confront = surfaces.find(
    (s) => s.intent === "confront" || s.focus === true
  );
  const others = surfaces.filter((s) => s !== confront);
  const compactIds = variationCompactIds(
    others.map((s) => ({ id: s.id, kind: s.kind, title: titleOf(s) }))
  );
  const isCompact = (s: SceneSurface) => compactIds.has(s.id);
  const { visible, hiddenCount } = partitionCards(others, isCompact, 3);
  const visibleCards = others.filter((s) => visible.includes(s.id) && !isCompact(s));
  const compactCards = others.filter(isCompact);

  // E2 收工叙事：progress 卡从未完成→完成的瞬间（真实数据派生）。
  const prevDoneRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    const doneIds = new Set(surfaces.filter(progressDone).map((s) => s.id));
    const newly = [...doneIds].filter((id) => !prevDoneRef.current.has(id));
    prevDoneRef.current = doneIds;
    if (newly.length > 0) setCompletionFx({ t0: Date.now() });
  }, [surfaces]);

  const fx: StageFx = {
    pulse,
    decision: decisionFx,
    completion: completionFx,
    listening,
  };
  // 入场 stagger（§12 batch 多卡 40ms）：新卡按到达序分配 40ms 槽位。
  // 渲染期调整模式（ref 记账，不触发重渲染）；既有卡不重播。
  const enterDelaysRef = useRef(new Map<string, number>());
  const prevIdsRef = useRef<Set<string>>(new Set());
  const fresh = surfaces.filter((s) => !prevIdsRef.current.has(s.id));
  if (fresh.length > 0) {
    fresh.forEach((s, i) => enterDelaysRef.current.set(s.id, i * 40));
    prevIdsRef.current = new Set(surfaces.map((s) => s.id));
  }
  const enterStyle = (id: string) => {
    const delay = enterDelaysRef.current.get(id) ?? 0;
    return {
      animation: `scene-card-in 320ms cubic-bezier(.2,.9,.25,1) ${delay}ms both`,
    };
  };
  const exitStyle = (reason: SceneExitReason) => {
    const name =
      reason === "done"
        ? "scene-card-out-done"
        : reason === "dismissed"
          ? "scene-card-out-dismissed"
          : reason === "superseded"
            ? "scene-card-out-superseded"
            : "scene-card-out-ttl";
    return { animation: `${name} 260ms ease-in both` };
  };

  return (
    <div data-testid="scene-stage" className="absolute inset-0">
      <SceneCosmos identityId={identityId} fx={fx} />

      {/* 隔夜账卡（§6）：左上钉顶，dismiss 当天不再合成 */}
      <div className="absolute left-6 top-5 z-20 w-[min(400px,42vw)]">
        <OvernightDigest identityId={identityId} tele={tele} lastSeenMs={lastSeenMs} />
      </div>

      {/* 线索行（mockup .threads）：左下，只读投影 */}
      <ThreadsRow identityId={identityId} />

      {/* 右列（分区制 §2）：整卡 ≤3 + 溢出 chip + 变奏/沉淀单行（玻璃浮层） */}
      <div className="absolute right-6 top-5 z-20 flex max-h-[calc(100%-7rem)] w-[316px] flex-col gap-3 overflow-y-auto">
        {visibleCards.map((s) => (
          <div
            key={s.id}
            data-testid="scene-card"
            data-scene-id={s.id}
            style={enterStyle(s.id)}
            className={cn(
              "rounded-2xl border border-line bg-card/86 p-4 shadow-[0_8px_28px_rgba(0,0,0,0.45)] backdrop-blur-[10px]",
              s.intent === "ambient" && "opacity-80"
            )}
          >
            <SurfaceCard surface={s} identityId={identityId} onDecision={(dir) => setDecisionFx({ dir, t0: Date.now() })} />
          </div>
        ))}
        {hiddenCount > 0 && (
          <button
            type="button"
            data-testid="scene-overflow"
            onClick={() => setExpanded(!expanded)}
            className="self-end rounded-full border border-border bg-card/95 px-3 py-1 font-mono text-[10px] text-muted-foreground transition-colors hover:text-foreground"
          >
            {expanded ? "收起 ⌃" : `还有 ${hiddenCount} 项 ⌄`}
          </button>
        )}
        {expanded &&
          others
            .filter((s) => !visible.includes(s.id) && !isCompact(s))
            .map((s) => (
              <div
                key={s.id}
                data-testid="scene-card"
                data-scene-id={s.id}
                className="rounded-2xl border border-line bg-card/86 p-4 shadow-[0_8px_28px_rgba(0,0,0,0.45)] backdrop-blur-[10px]"
              >
                <SurfaceCard surface={s} identityId={identityId} />
              </div>
            ))}
        {compactCards.map((s) => (
          <div
            key={s.id}
            data-testid="scene-card"
            data-scene-id={s.id}
            data-compact="true"
            className="rounded-xl border border-border/70 bg-card/85 px-3 py-2"
          >
            <SurfaceCard surface={s} identityId={identityId} compact />
          </div>
        ))}
        {/* TTL 沉淀条（§16.2）：单行摘要，不占 3 张配额，隔夜并入账卡 */}
        {sedimented.map(({ surface }) => (
          <div
            key={`sed-${surface.id}`}
            data-testid="scene-sediment"
            data-scene-id={surface.id}
            data-compact="sediment"
            className="rounded-xl border border-border/50 bg-card/60 px-3 py-2 opacity-70"
          >
            <SurfaceCard surface={surface} identityId={identityId} compact />
          </div>
        ))}
        {/* 退场中的卡（§16.7）：按 reason 播退场动效后由宿主清理 */}
        {leaving.map(({ surface, reason, key }) => (
          <div
            key={`leave-${key}`}
            data-testid="scene-card-leaving"
            style={exitStyle(reason)}
            className="pointer-events-none rounded-2xl border border-line bg-card/86 p-4"
          >
            <SurfaceCard surface={surface} identityId={identityId} compact />
          </div>
        ))}
      </div>

      {/* confront 全局遮罩（§2：中央独占，宇宙压暗由遮罩承担） */}
      {confront && (
        <div className="absolute inset-0 z-30 flex items-center justify-center bg-background/60 p-6 backdrop-blur-[2px]">
          <div
            data-testid="scene-card"
            data-scene-id={confront.id}
            style={enterStyle(confront.id)}
            className="w-full max-w-[460px] rounded-2xl border border-primary/40 bg-card p-6 shadow-[0_24px_70px_rgba(0,0,0,0.6)]"
          >
            <SurfaceCard surface={confront} identityId={identityId} onDecision={(dir) => setDecisionFx({ dir, t0: Date.now() })} />
          </div>
        </div>
      )}
      {/* 退场中的 confront 卡：遮罩已撤，居中播收合动效 */}
      {leaving
        .filter((l) => l.surface.intent === "confront" || l.surface.focus)
        .map(({ surface, reason, key }) => (
          <div
            key={`leave-c-${key}`}
            className="pointer-events-none absolute inset-0 z-30 flex items-center justify-center"
          >
            <div
              data-testid="scene-card-leaving"
              style={exitStyle(reason)}
              className="mx-4 w-full max-w-[460px] rounded-2xl border border-border bg-card p-6"
            >
              <SurfaceCard surface={surface} identityId={identityId} />
            </div>
          </div>
        ))}

      {/* 底部：心跳行 + 对话条（§3 对话单真相源 + §7 时间感），居中 */}
      <div className="absolute bottom-[4.5rem] left-1/2 z-20 w-[min(620px,60vw)] -translate-x-1/2">
        <WorkHeartbeat tele={tele} />
      </div>
      <div className="absolute bottom-6 left-1/2 z-20 w-[min(620px,60vw)] -translate-x-1/2">
        <TalkStrip
          identityId={identityId}
          onExpand={onExpand}
          onListeningChange={setListening}
        />
      </div>
    </div>
  );
}

/** SceneBadge 是「返回工作区之后」的存在感提示：Agent 又发了
 * inform/ambient 卡但不抢屏——呼吸点 + 张数，点它回舞台。 */
export function SceneBadge({
  count,
  onOpen,
}: {
  count: number;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      data-testid="scene-badge"
      onClick={onOpen}
      className="fixed right-4 top-16 z-40 flex items-center gap-2 rounded-full border border-primary/40 bg-card/95 px-3 py-1.5 text-xs text-foreground shadow-lg transition-colors hover:text-primary"
    >
      <span className="relative flex size-2">
        <span className="absolute inline-flex size-2 animate-ping rounded-full bg-primary/60" />
        <span className="relative inline-flex size-2 rounded-full bg-primary" />
      </span>
      Agent 舞台 · {count} 张卡片
    </button>
  );
}
