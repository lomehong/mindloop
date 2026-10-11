/** 场景卡片层（scene.md §7 渲染端）：订阅场景流，把服务端折叠好的
 * 场景状态投影成浮层——inform/ambient 卡右上角堆叠，confront 卡居中
 * 聚焦（遮罩）。渲染端只有投影与过渡，没有业务决策：卡片上的按钮
 * 只上报 intent（Phase 2 接 /scene/intent），Phase 1 只读展示。
 * kind 注册表：不认识的 kind 降级为 text 占位（title=kind），不得
 * 崩溃——对应协议"shell 不认识的 kind 应当降级渲染或忽略"。 */
import { useEffect, useRef, useState } from "react";
import { openSceneStream, type SceneSurface, type SceneState } from "~/lib/scene";
import { cn } from "~/lib/utils";

const EMPTY_SCENE: SceneState = { rev: 0, surfaces: [] };

/** useSceneStream 维护一条场景流的生命周期：连接 → 状态折叠 → 断线
 * 重连（2s 起步指数退避，封顶 15s）。identityId 变化即重建（切身份
 * = 换一条流）。visibility 无关：卡片面在任何 tab 都在场。 */
function useSceneStream(identityId: string): SceneState {
  const [state, setState] = useState<SceneState>(EMPTY_SCENE);
  useEffect(() => {
    if (!identityId) return;
    const controller = new AbortController();
    let stopped = false;
    let backoff = 2000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const connect = () => {
      if (stopped) return;
      openSceneStream(identityId, setState, { signal: controller.signal })
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

/** SurfaceCard 是 kind 注册表：每种 kind 一个渲染函数。data 是纯
 * 展示数据——组件不 eval、不注入 DOM，React 默认转义即达标。 */
function SurfaceCard({ surface }: { surface: SceneSurface }) {
  const { kind, data, summary } = surface;
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

/** SceneLayer 是身份页的全局卡片面：挂在 identity-shell 的 Outlet
 * 旁，任何 tab 都在场。confront 卡居中聚焦（遮罩），其余右上角
 * 堆叠；同一时刻至多一张 confront（协议约束 focus 唯一）。 */
export function SceneLayer({ identityId }: { identityId: string }) {
  const state = useSceneStream(identityId);
  const prevRev = useRef(0);
  // rev 无前进说明流未就绪：不渲染（避免闪空层）。
  if (state.rev === 0 || state.rev < prevRev.current) return null;
  prevRev.current = state.rev;

  const confront = state.surfaces.find(
    (s) => s.intent === "confront" || s.focus === true
  );
  const others = state.surfaces.filter((s) => s !== confront);

  return (
    <>
      {others.length > 0 && (
        <div className="pointer-events-none fixed right-4 top-16 z-50 flex w-80 flex-col gap-2">
          {others.map((s) => (
            <div
              key={s.id}
              className={cn(
                "pointer-events-auto rounded-xl border border-border bg-card/95 p-3 shadow-lg backdrop-blur",
                s.intent === "ambient" && "opacity-75"
              )}
            >
              <SurfaceCard surface={s} />
            </div>
          ))}
        </div>
      )}
      {confront && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 backdrop-blur-sm">
          <div className="mx-4 w-full max-w-md rounded-xl border border-border bg-card p-5 shadow-2xl">
            <SurfaceCard surface={confront} />
          </div>
        </div>
      )}
    </>
  );
}
