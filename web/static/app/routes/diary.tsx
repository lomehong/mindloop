// 日记（avatar.md §5 时间望远镜）：缩放即时间旅行——此刻 → 1h 步骤
// 粒子 → 24h 生命线 → 14 天年轮/章节，右缘刻度尺常驻 + 滚轮缩放
// （350ms 节流，离散档位永远可预期）。收编而非替代：泳道（/traj）
// 保留 TimelineView 全量能力做显微镜档，粒子与生命线点击都跳
// ?step= 深链——审计精度一个不减；?step=/?run= 深链语义不归本页管。
//
// 窗口诚实：轨迹窗口是 mindlog tail=1000（与工作台共享缓存），1h/
// 24h 面板按覆盖起点如实标注「自 HH:MM 起」；14 天年轮来自 usage
// 台账（天粒度），章节来自 recap（与周报同源——叙事与账本一致）。

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useEffect, useMemo, useRef } from "react";

import { VitalStrip } from "~/components/vital-strip";
import {
  chaptersOf,
  coveredSince,
  DIARY_ZOOMS,
  particlesInWindow,
  ring14,
  stepZoom,
  WHEEL_THROTTLE_MS,
  type DiaryZoom,
} from "~/lib/diary";
import { fetchActivity, fetchIdentities, fetchMindlog, fetchRecap, fetchUsage } from "~/lib/api";
import { formatClock, formatDateTime } from "~/lib/format";
import { useNowTicker } from "~/lib/use-chat";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 日记" }];
}

const LEDGER_TAIL = 1000;

function zoomIndex(z: DiaryZoom): number {
  return DIARY_ZOOMS.findIndex((it) => it.key === z);
}

export default function Diary() {
  const { identityId = "" } = useParams();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const zoom = stepZoom(
    zoomIndex((params.get("zoom") as DiaryZoom) ?? "24h") === -1
      ? 2
      : zoomIndex((params.get("zoom") as DiaryZoom) ?? "24h")
  );

  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
  });
  const me = identities?.find((it) => it.id === identityId);
  const { data: mindlog } = useQuery({
    queryKey: ["ledger-steps", identityId],
    queryFn: () => fetchMindlog(identityId, { tail: LEDGER_TAIL }),
    refetchInterval: 60_000,
  });
  const { data: usage } = useQuery({
    queryKey: ["usage", identityId],
    queryFn: () => fetchUsage(identityId),
    refetchInterval: 30_000,
  });
  const { data: recap } = useQuery({
    queryKey: ["recap", identityId],
    queryFn: () => fetchRecap(identityId),
    refetchInterval: 120_000,
  });
  const { data: activity } = useQuery({
    queryKey: ["activity", identityId],
    queryFn: () => fetchActivity(identityId),
    refetchInterval: 30_000,
  });

  const steps = useMemo(() => mindlog?.steps ?? [], [mindlog]);
  // 粒子/覆盖判定需要"现在"——渲染期不裸调 Date.now()（纯性门禁），
  // 用 60s 一拍的 ticker（日记粒度足够）。
  const now = useNowTicker(true, 60_000);
  const setZoom = (z: DiaryZoom) => {
    const next = new URLSearchParams(params);
    next.set("zoom", z);
    setParams(next, { replace: true });
  };

  // 滚轮=时间旅行：向上拉远、向下拉近（350ms 节流；离散档位——
  // reduced-motion 与常态同一行为，缩放永远可预期）。
  // 守卫（main ecd818b 同款问题的同款修法，抄 scene-cosmos）：事件
  // 目标落在可滚动容器（生命线卡片长列表等）或交互元素上时放行——
  // 缩放不得独占滚轮，否则页内滚动区滚不动。
  const rootRef = useRef<HTMLDivElement>(null);
  const wheelLock = useRef(0);
  useEffect(() => {
    const host = rootRef.current;
    if (!host) return;
    const scrollableAncestor = (el: Element | null): boolean => {
      for (let n = el; n && n !== host; n = n.parentElement) {
        const s = getComputedStyle(n);
        if (/(auto|scroll)/.test(s.overflowY) && n.scrollHeight > n.clientHeight + 1) {
          return true;
        }
      }
      return false;
    };
    const onWheel = (e: WheelEvent) => {
      const target = e.target as Element | null;
      if (
        target?.closest("a,button,input,textarea") ||
        scrollableAncestor(target)
      ) {
        return;
      }
      const t = Date.now();
      if (t - wheelLock.current < WHEEL_THROTTLE_MS) return;
      wheelLock.current = t;
      const i = zoomIndex(zoom);
      const next = e.deltaY < 0 ? i + 1 : i - 1;
      if (next < 0 || next >= DIARY_ZOOMS.length) return;
      e.preventDefault();
      setZoom(stepZoom(next));
    };
    host.addEventListener("wheel", onWheel, { passive: false });
    return () => host.removeEventListener("wheel", onWheel);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom, params]);

  const jumpToStep = (stepId: string) =>
    `/i/${encodeURIComponent(identityId)}/traj?step=${encodeURIComponent(stepId)}`;

  const hour1 = useMemo(
    () => particlesInWindow(steps, now, 3_600_000),
    // now 是 60s 一拍的 ticker——随它滚动粒子窗正是想要的语义。
    [steps, now]
  );
  const hour1Covered = coveredSince(steps, now, 3_600_000);
  const dayCovered = coveredSince(steps, now, 24 * 3_600_000);
  const rings = useMemo(() => ring14(usage?.daily), [usage]);
  const chapters = useMemo(() => chaptersOf(recap, now), [recap, now]);
  const coverNote = (covered: boolean) =>
    covered
      ? `覆盖完整（窗口 ${LEDGER_TAIL} 步）`
      : `窗口不足 24h——自 ${
          mindlog?.steps?.[0]?.ts
            ? formatClock(mindlog.steps[0].ts).slice(0, 5)
            : "—"
        } 起，更早的在泳道`;

  return (
    <div ref={rootRef} className="absolute inset-0 overflow-hidden">
      {/* 沉浸氛围（样张 s-diary：cosmos 星空压暗）——日记是时间旅行，
          不用引擎室的纸感。 */}
      <div
        aria-hidden
        className="absolute inset-0 opacity-60"
        style={{
          background:
            "radial-gradient(1px 1px at 12% 22%, rgba(230,236,225,.5) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 78% 14%, rgba(230,236,225,.4) 50%, transparent 51%)," +
            "radial-gradient(1.5px 1.5px at 62% 74%, rgba(230,236,225,.35) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 30% 68%, rgba(230,236,225,.4) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 88% 55%, rgba(230,236,225,.3) 50%, transparent 51%)," +
            "radial-gradient(1.5px 1.5px at 44% 32%, rgba(183,155,209,.4) 50%, transparent 51%)," +
            "radial-gradient(1px 1px at 8% 82%, rgba(230,236,225,.3) 50%, transparent 51%)",
        }}
      />
      {/* 右缘时间刻度尺（常驻）：缩放永远可预期。 */}
      <div className="absolute right-1 top-1/2 z-20 flex -translate-y-1/2 flex-col items-end gap-1 rounded-xl border border-border bg-card/90 p-2 backdrop-blur">
        <span className="px-2 pb-0.5 font-mono text-[10px] text-faint">拉远 ↑</span>
        {DIARY_ZOOMS.map((z) => (
          <button
            key={z.key}
            type="button"
            title={z.hint}
            onClick={() => setZoom(z.key)}
            aria-current={zoom === z.key ? "true" : undefined}
            className={cn(
              "rounded-lg px-2.5 py-1 text-right font-mono text-[11px] transition-colors",
              zoom === z.key
                ? "border border-border bg-muted text-foreground"
                : "border border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {z.label}
          </button>
        ))}
        <span className="px-2 pt-0.5 font-mono text-[10px] text-faint">继续拉远 ↓</span>
        <Link
          to="/galaxy"
          className="rounded-lg px-2.5 py-1 text-right font-mono text-[11px] text-muted-foreground hover:text-foreground"
          title="星系 · 多心智远景"
        >
          星系
        </Link>
      </div>

      <div className="absolute inset-0 overflow-y-auto px-10 py-8 pr-28">
        {zoom === "now" && (
          <div className="mx-auto mt-10 max-w-xl rounded-xl border border-border bg-card p-5">
            <div className="text-[15px] font-medium">此刻舞台</div>
            <p className="mt-1.5 text-[12.5px] text-muted-foreground">
              对话 · 卡片 · 线索——从这里拉远，就是翻它的日记。
            </p>
            <div className="mt-3 flex items-center gap-3">
              <Link
                to={`/i/${encodeURIComponent(identityId)}`}
                className="rounded-lg border border-primary/40 px-3.5 py-1.5 text-[13px] text-primary hover:bg-primary/10"
              >
                回舞台 →
              </Link>
              <span className="font-mono text-[11px] text-faint">
                {activity?.last_step_age_s !== null && activity?.last_step_age_s !== undefined
                  ? `最近一步 ${Math.round(activity.last_step_age_s / 60)} 分钟前`
                  : "暂无活动读数"}
              </span>
            </div>
          </div>
        )}

        {zoom === "1h" && (
          <div className="rounded-xl border border-line-strong bg-card/85 p-4 backdrop-blur-[6px]">
            <div className="mb-3 flex flex-wrap items-baseline gap-2">
              <h2 className="text-[14px] font-medium">最近 1 小时 · 步骤粒子</h2>
              <span className="font-mono text-[10.5px] text-faint">
                五色分类学 · {hour1.particles.length} 粒
                {hour1.overflow > 0 && ` · 另有 ${hour1.overflow} 粒折叠`} · {coverNote(hour1Covered)}
              </span>
            </div>
            {hour1.particles.length === 0 ? (
              <p className="py-2 text-[12.5px] text-faint">这一小时没有步骤——安睡或安静。</p>
            ) : (
              <div className="flex flex-wrap gap-[5px]">
                {hour1.particles.map((p) => (
                  <Link
                    key={p.stepId}
                    to={jumpToStep(p.stepId)}
                    title={p.title}
                    className={cn("size-2.5 rounded-full transition-transform hover:scale-150", p.dot)}
                  />
                ))}
              </div>
            )}
            <p className="mt-3 text-[11.5px] text-faint">
              点任何粒子 = ?step= 深链直达步级审计（显微镜档在泳道）。
            </p>
          </div>
        )}

        {zoom === "24h" && (
          <div className="rounded-xl border border-line-strong bg-card/85 p-4 backdrop-blur-[6px]">
            <div className="mb-3 flex flex-wrap items-baseline gap-2">
              <h2 className="text-[14px] font-medium">24 小时生命线</h2>
              <span className="font-mono text-[10.5px] text-faint">
                柱高=步骤密度 · 陶土=错误 · 湖=消息 · 空段=安睡（不猜）· {coverNote(dayCovered)}
              </span>
            </div>
            <VitalStrip
              steps={steps}
              live={me?.live ?? false}
              onJump={(step) => navigate(jumpToStep(step.step_id))}
            />
            <p className="mt-2 text-[11.5px] text-faint">
              点柱 = 跳到该时刻的泳道位置（?step= 深链）。
            </p>
          </div>
        )}

        {zoom === "14d" && (
          <div className="space-y-3">
            <div className="rounded-xl border border-line-strong bg-card/85 p-4 backdrop-blur-[6px]">
              <div className="mb-2 flex flex-wrap items-baseline gap-2">
                <h2 className="text-[14px] font-medium">14 天年轮</h2>
                <span className="font-mono text-[10.5px] text-faint">
                  每天一柱 · 有柱=那天有调用 · 空段=休息（usage 台账，与食量同源）
                </span>
              </div>
              <div className="flex h-[64px] items-end gap-[3px]">
                {rings.map((d) => (
                  <i
                    key={d.day}
                    title={`${d.day} · ${d.calls} 次调用`}
                    className={cn(
                      "flex-1 rounded-t-[2px]",
                      d.calls > 0 ? "bg-resin opacity-85" : "bg-muted"
                    )}
                    style={{ height: d.calls > 0 ? undefined : 2 }}
                  />
                ))}
              </div>
            </div>
            <div className="rounded-xl border border-line-strong bg-card/85 p-4 backdrop-blur-[6px]">
              <div className="mb-3 flex flex-wrap items-baseline gap-2">
                <h2 className="text-[14px] font-medium">章节（14 天内）</h2>
                <span className="font-mono text-[10.5px] text-faint">
                  与分集/周报同源数据——叙事与账本一致
                </span>
              </div>
              {chapters.length === 0 ? (
                <p className="py-2 text-[12.5px] text-faint">
                  14 天内没有章节（分集由心智的 recap 沉淀生成）。
                </p>
              ) : (
                <div className="grid gap-3 min-[880px]:grid-cols-3">
                  {chapters.slice(0, 6).map((c) => (
                    <div key={c.idx} className="rounded-lg border border-border p-3">
                      <div className="font-mono text-[10.5px] text-faint">
                        {formatDateTime(c.firstTs).slice(5, 10)} ~ {formatDateTime(c.lastTs).slice(5, 10)} · {c.nSteps} 步
                      </div>
                      <div className="mt-1 line-clamp-2 text-[13px] font-medium">{c.title}</div>
                      {c.summary && (
                        <div className="mt-1 line-clamp-3 text-[12px] text-muted-foreground">{c.summary}</div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
