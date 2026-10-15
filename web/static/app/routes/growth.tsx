// 成长档案（avatar.md §4 进化篇：同行曲线 · 自主权边界 · 传承清单 ·
// 同行账）。裁决记录（avatar.md §10）：v1 全只读——调整走 CLI/env
// （调整本身有留痕），界面内可调面板需要新写端点，违背后端零改动硬
// 边界；传承清单仅偏好类证据（preference 记忆 + taste 七天聚合），
// persona 无事件出处不纳入。
//
// 机制账（§3.2/§10.1）：曲线 marker 是授权档的投影（规则公开可查），
// 不是成长测量——绝不显示百分比进度；边界只在用户显式调整时移动，
// 绝不自动放宽（§8.3 放权可逆）。

import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { useMemo } from "react";

import { LedgerColumn } from "~/components/ledger-column";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  fetchApprovals,
  fetchBudget,
  fetchIdentityEnv,
  fetchIdentities,
  fetchMemories,
  fetchMindlog,
  fetchSensors,
  fetchTaste,
} from "~/lib/api";
import {
  ledgerFor,
  mergeLedgers,
  parsePolicyMode,
  type IdentityLedger,
} from "~/lib/growth-ledger";
import { learningCapTotal } from "~/lib/attention";
import {
  boundaryReadings,
  curvePositionOf,
  CURVE_SEGMENTS,
  legacyEvidence,
  type PolicyMode,
} from "~/lib/growth-dossier";
import { formatDateTime } from "~/lib/format";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 成长档案" }];
}

const LEDGER_TAIL = 1000;
const LEDGER_POLL_MS = 60_000;

function Section({
  title,
  note,
  children,
  className,
}: {
  title: string;
  note?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("rounded-xl border border-border bg-card", className)}>
      <div className="flex flex-wrap items-baseline gap-2 border-b border-border px-4 py-2.5">
        <h2 className="text-[14px] font-medium">{title}</h2>
        {note && <span className="text-[11.5px] text-faint">{note}</span>}
      </div>
      <div className="px-4 py-3">{children}</div>
    </section>
  );
}

/** 同行曲线三段带 + 授权档投影 marker（投影规则公开，非测量）。 */
function CompanionCurve({ mode }: { mode: PolicyMode }) {
  const pos = curvePositionOf(mode);
  return (
    <div>
      <div className="relative mt-3 mb-1.5 flex h-4 overflow-visible rounded-lg">
        {CURVE_SEGMENTS.map((seg, i) => (
          <div
            key={seg.key}
            className={cn(
              "h-full opacity-55",
              i === 0 && "rounded-l-lg bg-moss",
              i === 1 && "bg-lake",
              i === 2 && "rounded-r-lg bg-resin"
            )}
            style={{ width: `${seg.width * 100}%` }}
            title={`${seg.key} · ${seg.desc}`}
          />
        ))}
        <div
          className="absolute -top-1 h-6 w-3.5 rounded-md border-2 border-background bg-foreground"
          style={{ left: `calc(${pos * 100}% - 7px)` }}
          title={`授权档投影：${mode}`}
        />
      </div>
      <div className="flex justify-between font-mono text-[10.5px] text-faint">
        {CURVE_SEGMENTS.map((seg) => (
          <span key={seg.key}>
            {seg.key}（{seg.desc}）
          </span>
        ))}
      </div>
      <p className="mt-2.5 text-[11.5px] text-faint">
        marker = 执行授权档在曲线上的投影（deny→几乎不放手 · ask→全审 ·
        auto→分级放行即起步→同行的边界 · trusted→直执行），不是成长测量——
        阶段推进只靠你放权，绝不自动放宽，模型不得自我加冕（avatar.md §4.4）。
      </p>
    </div>
  );
}

export default function Growth() {
  const { identityId = "" } = useParams();
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
  });
  const me = identities?.find((it) => it.id === identityId);
  const { data: env } = useQuery({
    queryKey: ["env", identityId],
    queryFn: () => fetchIdentityEnv(identityId),
  });
  const { data: sensors } = useQuery({
    queryKey: ["sensors", identityId],
    queryFn: () => fetchSensors(identityId),
  });
  const { data: memories } = useQuery({
    queryKey: ["memories", identityId],
    queryFn: () => fetchMemories(identityId),
    refetchInterval: LEDGER_POLL_MS,
  });
  const { data: taste } = useQuery({
    queryKey: ["taste", identityId],
    queryFn: () => fetchTaste(identityId),
    refetchInterval: LEDGER_POLL_MS,
  });
  const { data: mindlog } = useQuery({
    queryKey: ["ledger-steps", identityId],
    queryFn: () => fetchMindlog(identityId, { tail: LEDGER_TAIL }),
    refetchInterval: LEDGER_POLL_MS,
  });
  // 每日总预算（budget-config.tsx 同源端点；键一致共享缓存）。
  // 走 fetchBudget（apiRequest：token 部署带鉴权头，同源守卫）。
  const { data: budget } = useQuery({
    queryKey: ["budget", identityId],
    queryFn: () => fetchBudget(identityId),
    staleTime: 30_000,
  });
  // 同行账的事件源（与工作台共享 ["ledger-steps"] 缓存）。
  const ledger: IdentityLedger | undefined = useMemo(
    () =>
      mindlog
        ? ledgerFor({
            identityId,
            identityName: me?.name ?? identityId,
            steps: mindlog.steps,
            memories,
          })
        : undefined,
    [mindlog, memories, identityId, me?.name]
  );
  const merged = useMemo(
    () => (ledger ? mergeLedgers([ledger]) : null),
    [ledger]
  );

  if (!identities) {
    return (
      <div className="space-y-3 pt-4">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-28 w-full rounded-xl" />
        <Skeleton className="h-48 w-full rounded-xl" />
      </div>
    );
  }

  const envVars: Record<string, string> = {};
  for (const e of env?.env ?? []) envVars[e.key] = e.value;
  const policy = parsePolicyMode(envVars["MINDLOOP_EXEC_POLICY"]);
  const rows = boundaryReadings({
    env: envVars,
    dailyLimit: budget?.daily_limit,
    sensors,
    learningCapped: mindlog ? learningCapTotal(mindlog.steps) : null,
  });
  const legacy = legacyEvidence(memories, taste);

  return (
    <div className="mx-auto w-full max-w-5xl space-y-3 pt-4">
      <Section
        title="同行曲线"
        note="阶段 = 自主权边界的当前快照 · 推进只靠你放权 · avatar.md §4.1"
      >
        <CompanionCurve mode={policy.mode ?? "auto"} />
      </Section>

      <div className="grid gap-3 lg:grid-cols-[1.2fr_1fr]">
        <Section
          title="自主权边界"
          note="只读 · 调整走 CLI/env（调整本身落痕）· 可随时收紧，绝不自动放宽"
        >
          <div className="flex flex-col">
            {rows.map((row) => (
              <div
                key={row.label}
                className="flex flex-wrap items-center gap-x-3 border-b border-border py-2 last:border-b-0"
              >
                <span className="w-28 shrink-0 text-[12.5px] text-muted-foreground">
                  {row.label}
                </span>
                {row.label === "执行授权档" ? (
                  // 样张 .seg 分段控件形态——只读显示：当前档高亮，
                  // 调整走 CLI/env（§10 裁决：界面内可调需要新端点）。
                  <span className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                    <span className="inline-flex overflow-hidden rounded-lg border border-line-strong">
                      {(
                        [
                          ["auto", "auto · 分级"],
                          ["ask", "ask · 全审"],
                          ["trusted", "trusted · 直执行"],
                          ["deny", "deny · 只思考"],
                        ] as const
                      ).map(([m, label]) => (
                        <span
                          key={m}
                          className={cn(
                            "px-2.5 py-1 font-mono text-[11px]",
                            policy.mode === m
                              ? "bg-primary/12 text-primary"
                              : "text-faint"
                          )}
                        >
                          {label}
                        </span>
                      ))}
                    </span>
                    {row.hint && (
                      <span className="font-mono text-[10.5px] text-faint">{row.hint}</span>
                    )}
                  </span>
                ) : (
                  <>
                    <span className="min-w-0 flex-1 text-[13px]">{row.value}</span>
                    {row.hint && (
                      <span className="font-mono text-[10.5px] text-faint">
                        {row.hint}
                      </span>
                    )}
                  </>
                )}
              </div>
            ))}
          </div>
          <p className="mt-2.5 text-[11.5px] text-faint">
            今日代决审计（decision=auto）在身份目录 run/approvals 的审计文件——
            逐条可溯，界面不重复呈现（后端零改动边界）。
          </p>
        </Section>

        <Section
          title="传承清单"
          note="只有记录在案的证据，没有相似度百分比 · avatar.md §4.2"
        >
          {legacy.preferences.length === 0 && !legacy.touches ? (
            <p className="py-1 text-[12.5px] text-faint">
              还没有记录在案的偏好——它在等你教。
            </p>
          ) : (
            <div className="flex flex-col">
              {legacy.touches && (
                <div className="border-b border-border py-2 text-[12.5px] text-muted-foreground">
                  近 7 天你过手{" "}
                  <span className="font-mono">
                    {legacy.touches.approves + legacy.touches.denies + legacy.touches.undoes}
                  </span>{" "}
                  次（批改放行 {legacy.touches.approves} · 否决 {legacy.touches.denies} ·
                  纠错 {legacy.touches.undoes}）
                </div>
              )}
              {legacy.preferences.slice(0, 6).map((m) => (
                <Link
                  key={m.name}
                  to={`/i/${encodeURIComponent(identityId)}/memories`}
                  className="group border-b border-border py-2 last:border-b-0"
                >
                  <div className="truncate text-[13px] group-hover:underline">
                    {m.summary || m.slug}
                  </div>
                  <div className="mt-0.5 font-mono text-[10.5px] text-faint">
                    偏好 · {m.created ? formatDateTime(m.created).slice(0, 10) : "日期缺档"} ·
                    看全文 → 手账
                  </div>
                </Link>
              ))}
              {legacy.preferences.length > 6 && (
                <div className="pt-1.5 text-[11.5px] text-faint">
                  另有 {legacy.preferences.length - 6} 条 → 手账
                </div>
              )}
            </div>
          )}
        </Section>
      </div>

      <Section
        title="同行账 · 今天互相成就了什么"
        note={
          ledger && !ledger.windowCoversToday
            ? `窗口 = mindlog 最近 ${LEDGER_TAIL} 步（起点在今日之内，更早未含）`
            : `窗口 = mindlog 最近 ${LEDGER_TAIL} 步`
        }
      >
        {merged ? (
          <div className="grid gap-4 min-[880px]:grid-cols-3">
            <LedgerColumn kicker="它 → 你" tone="text-moss" events={merged.itForYou} />
            <LedgerColumn kicker="你 → 它" tone="text-lake" events={merged.youForIt} />
            <LedgerColumn kicker="一起长出" tone="text-resin" events={merged.together} />
          </div>
        ) : (
          <p className="py-1 text-[12.5px] text-faint">
            账目读数缺席（mindlog 不可达）。
          </p>
        )}
        {/* 审批待办在需要你条与任务页；此处只给提示，不放第二操作入口。 */}
        <ApprovalsHint identityId={identityId} />
      </Section>
    </div>
  );
}

function ApprovalsHint({ identityId }: { identityId: string }) {
  const { data: approvals } = useQuery({
    queryKey: ["approvals", identityId],
    queryFn: () => fetchApprovals(identityId),
    refetchInterval: 30_000,
  });
  if (!approvals || approvals.length === 0) return null;
  return (
    <p className="mt-2.5 text-[11.5px] text-resin">
      有 {approvals.length} 条脚本等你拍板 → 任务页（批改即放权证据）。
    </p>
  );
}
