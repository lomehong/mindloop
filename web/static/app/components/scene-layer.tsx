/** 场景卡片层（scene.md §7 渲染端）：订阅场景流，把服务端折叠好的
 * 场景状态投影成身份页内容区的「舞台」——Agent 驱动 UI 有卡时接管
 * 整块圆角内容区（tab 内容收起），撤卡后回归工作区；操作员可手动
 * 「返回工作区」，此后 inform/ambient 新卡只给呼吸角标不抢屏，
 * confront（必须停下决策）始终直接接管。渲染端只有投影与过渡，没
 * 有业务决策：卡片上的按钮只上报 intent（POST /scene/intent）。
 * kind 注册表：不认识的 kind 降级为 text 占位（title=kind），不得
 * 崩溃——对应协议"shell 不认识的 kind 应当降级渲染或忽略"。 */
import { useEffect, useRef, useState } from "react";
import { openSceneStream, postSceneIntent, type SceneSurface, type SceneState } from "~/lib/scene";
import { cn } from "~/lib/utils";

const EMPTY_SCENE: SceneState = { rev: 0, surfaces: [] };

/** useSceneStream 维护一条场景流的生命周期：连接 → 状态折叠 → 断线
 * 重连（2s 起步指数退避，封顶 15s）。identityId 变化即重建（切身份
 * = 换一条流）。visibility 无关：卡片面在任何 tab 都在场。 */
export function useSceneStream(identityId: string): SceneState {
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
function SurfaceCard({ surface, identityId }: { surface: SceneSurface; identityId: string }) {
  const { kind, data, summary } = surface;
  if (kind === "choice") {
    return <ChoiceCard surface={surface} identityId={identityId} />;
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

/** ChoiceCard 是 confront 决策卡（scene.md §6 审批桌面化）：选项
 * 点击即 intent 回流（POST /scene/intent），审批类 surface 由后端
 * 换回 hash 走 policy.Decide。点击后本地锁定（"已提交，等待执行"
 *）——卡片本身的消失由服务端撤卡驱动（WaitHook 离开回调），前端
 * 不猜测结果。 */
function ChoiceCard({ surface, identityId }: { surface: SceneSurface; identityId: string }) {
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState("");
  const options = Array.isArray(surface.data?.options)
    ? (surface.data?.options as Array<{ value: string; label: string; tone?: string }>)
    : [];
  const promptText =
    typeof surface.data?.prompt === "string" && surface.data?.prompt
      ? (surface.data?.prompt as string)
      : surface.summary;

  const submit = async (value: string) => {
    if (pending) return;
    setPending(value);
    try {
      await postSceneIntent(identityId, {
        surface: surface.id,
        name: "select",
        data: { value },
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPending(null);
    }
  };

  return (
    <div>
      <div className="mb-2 text-[13px] font-medium text-foreground">{promptText}</div>
      {typeof (surface.data?.context as Record<string, unknown> | undefined)?.script === "string" && (
        <pre className="mb-3 max-h-40 overflow-auto rounded-md bg-muted p-2 text-[11px] leading-relaxed text-foreground/80">
          {(surface.data?.context as Record<string, unknown>).script as string}
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

/** SceneStage 是仪表盘的舞台形态：Agent 驱动 UI 接管整块内容区——
 * confront 卡居中聚焦（存在感最高），inform/ambient 卡按栅格排布
 * 弱化呈现。舞台自带返回工作区的开关（操作员随时收回视线）。
 * 定位：identity-shell 在舞台态以 absolute inset-0 挂本组件——铺满
 * 滚动容器的可视区（root.tsx 的 relative 锚），与圆角内容卡严丝合
 * 缝，高度不靠猜（猜必有缝，2026-10-12 实测）。 */
export function SceneStage({
  surfaces,
  identityId,
  onBack,
}: {
  surfaces: SceneSurface[];
  identityId: string;
  onBack: () => void;
}) {
  const confront = surfaces.find(
    (s) => s.intent === "confront" || s.focus === true
  );
  const others = surfaces.filter((s) => s !== confront);
  return (
    <div data-testid="scene-stage" className="flex h-full flex-col bg-card">
      <div className="flex items-center gap-3 border-b border-border/70 px-5 py-3">
        <span className="font-mono text-[11px] tracking-[0.14em] text-primary">
          AGENT 舞台
        </span>
        <span className="text-xs text-muted-foreground">
          Agent 正在驱动这块仪表盘 · {surfaces.length} 张卡片
        </span>
        <span className="flex-1" />
        <button
          type="button"
          data-testid="scene-stage-back"
          onClick={onBack}
          className="rounded-md border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          返回工作区
        </button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 overflow-y-auto p-8">
        {confront && (
          <div
            data-testid="scene-card"
            data-scene-id={confront.id}
            className="w-full max-w-xl rounded-2xl border border-primary/40 bg-card p-6 shadow-2xl ring-1 ring-primary/20"
          >
            <SurfaceCard surface={confront} identityId={identityId} />
          </div>
        )}
        {others.length > 0 && (
          <div className="grid w-full max-w-3xl grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-4">
            {others.map((s) => (
              <div
                key={s.id}
                data-testid="scene-card"
                data-scene-id={s.id}
                className={cn(
                  "rounded-xl border border-border bg-card/95 p-4",
                  s.intent === "ambient" && "opacity-75"
                )}
              >
                <SurfaceCard surface={s} identityId={identityId} />
              </div>
            ))}
          </div>
        )}
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
