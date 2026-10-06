// 工作台（/，docs/designs/ui-language.md §5.1）：左栏负责「你是谁」，
// 本页负责「发生了什么、要我做什么」。结构 = masthead（工作台 · N 具 ·
// 运行中 M + 批量操作）→ 需要你（全站唯一告警面：待审批/预算≥80%/熔断
// 冷却/stalled 四类信号聚合）→ 心智卡网格（≤6 具 3 列，更多降级列表行：
// 小像 · 状态 · 最近活动 · 14 天年轮条 · 今日读数 · 动作）。数据全部来自
// 既有 fetch；查询静默失败退化为「无读数/无信号」，不打断页面。

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, MoreHorizontal, Play, Plus, Square, Upload } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { fmtDuration } from "~/components/activity-badge";
import { AuthenticatedDownload } from "~/components/authenticated-download";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { CreatureSvg } from "~/components/pet/creature";
import { QueryErrorBanner } from "~/components/query-error-banner";
import {
  useControlsEnabled,
  useThinkerMutation,
} from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  createIdentity,
  deleteIdentity,
  exportAllUrl,
  exportIdentityUrl,
  fetchActivity,
  fetchApprovals,
  fetchIdentities,
  fetchUsage,
  importIdentities,
  killAll,
} from "~/lib/api";
import { formatClock, formatCount, formatRelativeTime } from "~/lib/format";
import { phaseFor } from "~/lib/pet-state";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import type {
  Identity,
  IdentityActivity,
  PendingApproval,
  Usage,
  UsageDay,
} from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 工作台" }];
}

function identityPath(id: string, rest = ""): string {
  return `/i/${encodeURIComponent(id)}${rest}`;
}

// --- 批量操作（masthead 右侧）------------------------------------------------

function NewIdentityForm() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: createIdentity,
    onSuccess: (created) => {
      toast.success(`已创建身份 ${created.name}`);
      queryClient.invalidateQueries({ queryKey: ["identities"] });
      setOpen(false);
      setName("");
      navigate(identityPath(created.id, "/run/thinkers"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (!open) {
    return (
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus className="size-3" />
        新建身份
      </Button>
    );
  }
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (name.trim()) mutation.mutate(name.trim());
      }}
    >
      <Input
        autoFocus
        value={name}
        onChange={(event) => setName(event.target.value)}
        placeholder="小写名字"
        pattern="[a-z0-9][a-z0-9-]*"
        title="小写字母数字与连字符"
        className="h-8 w-44 font-mono text-xs"
      />
      <Button type="submit" size="sm" disabled={mutation.isPending || !name.trim()}>
        创建
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => setOpen(false)}
      >
        取消
      </Button>
    </form>
  );
}

function ImportIdentityForm() {
  const [open, setOpen] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: ({ file, name }: { file: File; name?: string }) =>
      importIdentities(file, name),
    onSuccess: (result) => {
      const names = result.imported.map((i) => i.name);
      toast.success(
        names.length === 1
          ? `已导入身份 ${names[0]}`
          : `已导入 ${names.length} 个身份：${names.join(", ")}`
      );
      queryClient.invalidateQueries({ queryKey: ["identities"] });
      setOpen(false);
      setFile(null);
      setName("");
      if (result.imported.length === 1)
        navigate(identityPath(result.imported[0].id, "/run/thinkers"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (!open) {
    return (
      <Button
        variant="ghost"
        size="sm"
        title="从归档导入身份"
        onClick={() => setOpen(true)}
      >
        <Upload className="size-3" />
        导入
      </Button>
    );
  }
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (file) mutation.mutate({ file, name: name.trim() || undefined });
      }}
    >
      <input
        ref={fileInput}
        type="file"
        accept=".tgz,.gz,application/gzip"
        className="w-56 text-xs file:mr-2 file:rounded-md file:border file:bg-transparent file:px-2 file:py-1 file:text-xs"
        onChange={(event) => setFile(event.target.files?.[0] ?? null)}
      />
      <Input
        value={name}
        onChange={(event) => setName(event.target.value)}
        placeholder="重命名（可选）"
        pattern="[a-z0-9][a-z0-9-]*"
        title="小写字母数字与连字符；仅用于单身份归档"
        className="h-8 w-40 font-mono text-xs"
      />
      <Button type="submit" size="sm" disabled={mutation.isPending || !file}>
        {mutation.isPending ? "导入中…" : "导入"}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => setOpen(false)}
      >
        取消
      </Button>
    </form>
  );
}

function KillAllButton() {
  const queryClient = useQueryClient();
  // 干跑（dry_run）的输出展示在确认对话框里；确认后才真正执行 killall。
  const [confirmSummary, setConfirmSummary] = useState<string | null>(null);
  const mutation = useMutation({
    mutationFn: killAll,
    onSuccess: (result) => {
      const summary = result.stdout.trim() || "没有找到运行中的进程。";
      if (result.dry_run) {
        setConfirmSummary(summary);
      } else {
        toast.success("全部停止完成", { description: summary });
        queryClient.invalidateQueries();
      }
    },
    onError: (error: Error) => toast.error(error.message),
  });

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        disabled={mutation.isPending}
        title="停止本机全部 mindloop 进程（调度器、agent、思考者步骤）"
        className="text-clay hover:bg-clay/10 hover:text-clay"
        onClick={() => mutation.mutate(true)}
      >
        <Square className="size-3" />
        全部停止
      </Button>
      <ConfirmDialog
        open={confirmSummary !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmSummary(null);
        }}
        tone="danger"
        title="停止本机全部 mindloop 进程？"
        description={confirmSummary ?? ""}
        confirmText="全部停止"
        onCancel={() => toast.info("已取消全部停止")}
        onConfirm={() => mutation.mutate(false)}
      />
    </>
  );
}

// --- 信号（需要你）-----------------------------------------------------------

interface SignalRow {
  key: string;
  name: string;
  text: string;
  to: string;
}

/** 单具身份的四类信号，顺序即文档顺序：待审批 → 预算 → 熔断 → 停滞。
 * 导出去给回归单测盯着链接去处（待审批必须落在桌面任务 tab）。 */
export function signalsFor(
  identity: Identity,
  usage: Usage | undefined,
  approvals: PendingApproval[] | undefined,
  activity: IdentityActivity | undefined
): SignalRow[] {
  const rows: SignalRow[] = [];
  const push = (kind: string, text: string, to: string) =>
    rows.push({ key: `${identity.id}:${kind}`, name: identity.name, text, to });
  const base = identityPath(identity.id);

  if (approvals && approvals.length > 0) {
    const oldest = approvals.reduce((a, b) =>
      Date.parse(a.created) <= Date.parse(b.created) ? a : b
    );
    const ageH = (Date.now() - Date.parse(oldest.created)) / 3_600_000;
    const wait =
      Number.isFinite(ageH) && ageH >= 1
        ? `已等待 ${Math.floor(ageH)} 小时`
        : "运行等待授权";
    push(
      "approval",
      `${approvals.length} 条脚本待审批 · ${wait}`,
      `${base}/tasks`
    );
  }

  const admission = usage?.admission;
  if (admission && admission.daily_limit > 0) {
    const pct = (admission.used_today / admission.daily_limit) * 100;
    if (pct >= 80) {
      push(
        "budget",
        `今日预算已用 ${Math.round(pct)}% · ${formatCount(admission.used_today)}/${formatCount(admission.daily_limit)} tokens`,
        `${base}/run/usage`
      );
    }
  }
  if (admission?.cooling_until && Date.parse(admission.cooling_until) > Date.now()) {
    push("cooling", `熔断冷却中 · 至 ${formatClock(admission.cooling_until)}`, `${base}/run/usage`);
  }
  if (activity?.state === "stalled") {
    push(
      "stalled",
      `忙碌但安静 ${fmtDuration(activity.last_step_age_s) ?? "?"} · 可能停滞`,
      `${base}/run/health`
    );
  }
  return rows;
}

function NeedYou({ rows, pending }: { rows: SignalRow[]; pending: boolean }) {
  if (pending) {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-border bg-card px-4 py-2.5">
        <span className="size-1.5 rounded-full bg-faint" />
        <span className="text-[12.5px] text-muted-foreground">正在汇总待办…</span>
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-border bg-card px-4 py-2.5">
        <span className="size-1.5 rounded-full bg-moss" />
        <span className="text-[12.5px] text-muted-foreground">没有需要你的事</span>
      </div>
    );
  }
  return (
    <section className="overflow-hidden rounded-xl border border-border bg-card">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <span className="size-1.5 rounded-full bg-resin" />
        <span className="font-mono text-[10.5px] tracking-[0.1em] text-faint">
          需要你
        </span>
        <span className="font-mono text-[11.5px] font-semibold text-resin">
          {rows.length}
        </span>
      </div>
      {rows.map((row) => (
        <Link
          key={row.key}
          to={row.to}
          className="grid grid-cols-[96px_1fr_auto] items-center gap-2.5 border-b border-border px-4 py-2.5 last:border-b-0 hover:bg-muted"
        >
          <span className="truncate text-[13px] font-medium">{row.name}</span>
          <span className="truncate text-[12.5px] text-muted-foreground">
            {row.text}
          </span>
          <span className="font-mono text-[10.5px] text-resin">去处理 →</span>
        </Link>
      ))}
    </section>
  );
}

// --- 卡内零件 -----------------------------------------------------------------

function StatusChip({ live }: { live: boolean }) {
  return live ? (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-primary/30 bg-primary/12 px-2.5 py-0.5 font-mono text-[11px] text-foreground">
      <span className="size-[5px] animate-pulse rounded-full bg-primary" />
      运行中
    </span>
  ) : (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-border bg-muted px-2.5 py-0.5 font-mono text-[11px] text-muted-foreground">
      <span className="size-[5px] rounded-full bg-faint" />
      已停止
    </span>
  );
}

/** 近 14 天年轮：每天一柱，有柱 = 那天有调用（心智动过），空段 = 休息。
 * 现台账只记调用与 token（runs 计数缺席），故以 calls 为活动信号。 */
function ringCalls(daily: [string, UsageDay][] | undefined): { day: string; calls: number }[] {
  const byDay = new Map(daily ?? []);
  const out: { day: string; calls: number }[] = [];
  for (let i = 13; i >= 0; i--) {
    const key = new Date(Date.now() - i * 86_400_000).toISOString().slice(0, 10);
    out.push({ day: key, calls: byDay.get(key)?.calls ?? 0 });
  }
  return out;
}

function RingSpark({ days, big }: { days: { day: string; calls: number }[]; big?: boolean }) {
  // 平方根刻度：同一张条形里 600 次与 50 次都要看得见（差值全靠高度会压扁近几天）。
  const max = Math.max(1, ...days.map((d) => d.calls));
  const h = big ? 34 : 22;
  const height = (v: number) =>
    v > 0 ? Math.max(3, Math.round((Math.sqrt(v) / Math.sqrt(max)) * h)) : 1;
  return (
    <div className={cn("flex items-end", big ? "h-[34px] gap-[3px]" : "h-[22px] gap-[2px]")}>
      {days.map((d) => (
        <i
          key={d.day}
          title={`${d.day} · ${d.calls} 次调用`}
          className={cn("flex-1 rounded-t-[2px]", d.calls > 0 && "bg-moss opacity-90")}
          style={{ height: height(d.calls) }}
        />
      ))}
    </div>
  );
}

function todayUsage(usage: Usage | undefined): UsageDay | undefined {
  const key = new Date().toISOString().slice(0, 10);
  return usage?.daily?.find(([day]) => day === key)?.[1];
}

function Readouts({
  usage,
  dispatcherRunning,
}: {
  usage: Usage | undefined;
  dispatcherRunning: boolean;
}) {
  const today = todayUsage(usage);
  const tok = today ? formatCount(today.in + today.out + today.think) : null;
  const calls = today ? today.calls : null;
  const k = "font-mono text-[10.5px] tracking-[0.05em] text-faint";
  const v = "text-right font-mono text-[11.5px] tabular-nums";
  return (
    <div className="grid grid-cols-[1fr_auto] gap-x-2 gap-y-[5px] border-t border-dashed border-border pt-2.5">
      <span className={k}>今日 token</span>
      <span className={cn(v, tok === null && "text-faint")}>{tok ?? "—"}</span>
      <span className={k}>今日调用</span>
      <span className={cn(v, calls === null && "text-faint")}>
        {calls === null ? "—" : `${calls} 次`}
      </span>
      <span className={k}>调度器</span>
      <span
        className={cn(
          v,
          dispatcherRunning ? "text-primary" : "text-faint"
        )}
      >
        {dispatcherRunning ? "运行中" : "已停止"}
      </span>
    </div>
  );
}

function activityLine(identity: Identity, activity?: IdentityActivity): string {
  const rel = identity.last_activity_ts
    ? formatRelativeTime(identity.last_activity_ts)
    : null;
  // 忙碌数优先取 activity 的实数（identities 的 thinkers_active 是
  // 「live 即全部活跃」的近似）；activity 静默失败时退回近似值。
  const busyCount = activity
    ? activity.busy_thinkers.length
    : identity.thinkers_active;
  const busy =
    identity.thinkers_total > 0
      ? `思考者 ${busyCount}/${identity.thinkers_total} 忙碌`
      : null;
  if (rel && rel !== "—") return `${rel}有活动${busy ? ` · ${busy}` : ""}`;
  return busy ?? "尚无活动记录";
}

/** 停止/启动该身份的调度器（names=[] 同旧名册语义）；Shift+点击 = 强制停止。 */
function DispatcherControl({
  identityId,
  running,
  compact,
}: {
  identityId: string;
  running: boolean;
  compact?: boolean;
}) {
  const mutation = useThinkerMutation(identityId);
  const [confirmStop, setConfirmStop] = useState<{ force: boolean } | null>(null);

  if (running) {
    return (
      <>
        {compact ? (
          <Button
            size="icon-sm"
            variant="ghost"
            className="text-clay hover:bg-clay/10 hover:text-clay"
            disabled={mutation.isPending}
            title="停止（Shift+点击：立即终止）"
            onClick={(event) => setConfirmStop({ force: event.shiftKey })}
          >
            <Square className="size-4" />
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={mutation.isPending}
            title="优雅停止：不再接收新触发，进行中步骤跑完（Shift+点击：立即终止）"
            className="border-clay/40 text-clay hover:bg-clay/10 hover:text-clay"
            onClick={(event) => setConfirmStop({ force: event.shiftKey })}
          >
            <Square className="size-3" />
            停止
          </Button>
        )}
        <ConfirmDialog
          open={confirmStop !== null}
          onOpenChange={(open) => {
            if (!open) setConfirmStop(null);
          }}
          tone="danger"
          title={confirmStop?.force ? "强制停止全部思考者？" : "停止全部思考者？"}
          description={
            confirmStop?.force
              ? "进行中的步骤将被立即终止，未完成的现场会丢失。"
              : "不再接收新触发；进行中的步骤跑完后全部安静。"
          }
          confirmText={confirmStop?.force ? "强制停止" : "全部停止"}
          onConfirm={() => {
            if (confirmStop)
              mutation.mutate({ action: "stop", names: [], force: confirmStop.force });
          }}
        />
      </>
    );
  }
  return compact ? (
    <Button
      size="icon-sm"
      variant="ghost"
      disabled={mutation.isPending}
      title="启动全部思考者"
      onClick={() => mutation.mutate({ action: "start", names: [] })}
    >
      <Play className="size-4" />
    </Button>
  ) : (
    <Button
      size="sm"
      variant="outline"
      disabled={mutation.isPending}
      title="启动全部思考者"
      onClick={() => mutation.mutate({ action: "start", names: [] })}
    >
      <Play className="size-3" />
      启动
    </Button>
  );
}

/** ⋯ = 日志 / 导出 / 删除身份（docs/designs/ui-language.md §5.1）。 */
function CardMenu({ identityId, name }: { identityId: string; name: string }) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => deleteIdentity(identityId),
    onSuccess: () => {
      toast.success(`已删除身份 ${name}`);
      setConfirming(false);
      void queryClient.invalidateQueries({ queryKey: ["identities"] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  return (
    <div className="relative">
      <Button
        variant="ghost"
        size="icon-sm"
        title="日志 · 导出 · 删除身份"
        aria-label="更多操作"
        onClick={() => setOpen((v) => !v)}
      >
        <MoreHorizontal className="size-4" />
      </Button>
      {open && (
        <>
          <button
            type="button"
            aria-label="关闭菜单"
            className="fixed inset-0 z-30 cursor-default"
            onClick={() => setOpen(false)}
          />
          <div className="absolute right-0 top-full z-40 mt-1 min-w-28 rounded-lg border border-border bg-popover p-1 shadow-md">
            <Link
              to={identityPath(identityId, "/log")}
              className="block rounded-md px-2.5 py-1.5 text-xs hover:bg-accent"
              onClick={() => setOpen(false)}
            >
              日志
            </Link>
            <div className="[&>span]:block">
              <AuthenticatedDownload
                url={exportIdentityUrl(identityId)}
                filename={`${name}.tgz`}
                variant="ghost"
                size="sm"
                className="h-auto w-full justify-start rounded-md px-2.5 py-1.5 text-xs font-normal"
              >
                导出
              </AuthenticatedDownload>
            </div>
            <button
              type="button"
              className="block w-full rounded-md px-2.5 py-1.5 text-left text-xs text-clay hover:bg-clay/10"
              onClick={() => {
                setOpen(false);
                setConfirming(true);
              }}
            >
              删除身份
            </button>
          </div>
        </>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        tone="danger"
        title={`删除身份 ${name}？`}
        description={`将永久删除它的全部轨迹、记忆与人格，以及它自己的密钥（.env）。\n运行中的心智会先被停止；此操作不可恢复（心智根与全局配置不受影响）。`}
        confirmText={mutation.isPending ? "删除中…" : "删除"}
        onConfirm={() => mutation.mutate()}
      />
    </div>
  );
}

// --- 卡 / 行 ------------------------------------------------------------------

interface SlotData {
  identity: Identity;
  usage: Usage | undefined;
  approvals: PendingApproval[] | undefined;
  activity: IdentityActivity | undefined;
}

function IdentityCard({
  slot,
  controlsEnabled,
}: {
  slot: SlotData;
  controlsEnabled: boolean;
}) {
  const { identity, usage } = slot;
  const ring = useMemo(() => ringCalls(usage?.daily), [usage]);
  return (
    <div className="flex flex-col gap-2 rounded-xl border border-border bg-card p-3.5 pb-3">
      <div className="flex items-center gap-2.5">
        <CreatureSvg size={30} phase={phaseFor(identity.id)} />
        <Link
          to={identityPath(identity.id)}
          className={cn(
            "truncate text-[14.5px] font-medium hover:underline",
            !identity.live && "text-muted-foreground"
          )}
        >
          {identity.name}
        </Link>
        <span className="ml-auto">
          <StatusChip live={identity.live} />
        </span>
      </div>
      <div className="-mt-1 text-[12px] text-muted-foreground">
        {activityLine(identity, slot.activity)}
      </div>
      <RingSpark days={ring} big />
      <div className="-mt-1 font-mono text-[10px] tracking-[0.08em] text-faint">
        近 14 天 · 年轮
      </div>
      <Readouts usage={usage} dispatcherRunning={identity.dispatcher?.running ?? false} />
      {/* 操作行恒为单行：调度钮用图标态（compact）压缩宽度，列数由容器宽度约束。 */}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button size="sm" asChild>
          <Link to={identityPath(identity.id)}>进入轨迹 →</Link>
        </Button>
        <Button size="sm" variant="outline" asChild>
          <Link to={identityPath(identity.id, "/chat")}>对话</Link>
        </Button>
        <div className="ml-auto flex items-center gap-1">
          {controlsEnabled && identity.thinkers_total > 0 && (
            <DispatcherControl
              identityId={identity.id}
              running={identity.dispatcher?.running ?? false}
              compact
            />
          )}
          <CardMenu identityId={identity.id} name={identity.name} />
        </div>
      </div>
    </div>
  );
}

function IdentityRow({
  slot,
  controlsEnabled,
}: {
  slot: SlotData;
  controlsEnabled: boolean;
}) {
  const { identity, usage } = slot;
  const ring = useMemo(() => ringCalls(usage?.daily), [usage]);
  const today = todayUsage(usage);
  const tok = today ? formatCount(today.in + today.out + today.think) : "—";
  return (
    <div className="flex items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0 hover:bg-muted">
      <Link
        to={identityPath(identity.id)}
        className="flex min-w-0 items-center gap-2.5"
      >
        <CreatureSvg size={22} phase={phaseFor(identity.id)} />
        <span
          className={cn(
            "truncate text-[13px] font-medium",
            !identity.live && "text-muted-foreground"
          )}
        >
          {identity.name}
        </span>
      </Link>
      <StatusChip live={identity.live} />
      <span className="hidden min-w-0 flex-1 truncate text-[12px] text-muted-foreground lg:block">
        {activityLine(identity, slot.activity)}
      </span>
      <div className="hidden w-24 shrink-0 md:block">
        <RingSpark days={ring} />
      </div>
      <span className="hidden whitespace-nowrap font-mono text-[11px] tabular-nums text-muted-foreground sm:block">
        {tok} tok · {today?.calls ?? 0} 次
      </span>
      {controlsEnabled && identity.thinkers_total > 0 && (
        <DispatcherControl
          identityId={identity.id}
          running={identity.dispatcher?.running ?? false}
          compact
        />
      )}
      <CardMenu identityId={identity.id} name={identity.name} />
    </div>
  );
}

// --- 骨架 ---------------------------------------------------------------------

function HomeSkeleton() {
  return (
    <div className="space-y-3.5">
      <div className="flex items-center gap-2.5">
        <Skeleton className="h-7 w-24" />
        <Skeleton className="h-4 w-44" />
        <span className="flex-1" />
        <Skeleton className="h-8 w-20" />
        <Skeleton className="h-8 w-20" />
        <Skeleton className="h-8 w-20" />
      </div>
      <Skeleton className="h-11 w-full rounded-xl" />
      <div className="grid gap-3 min-[880px]:grid-cols-2 min-[1440px]:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <div
            key={i}
            className="flex flex-col gap-2.5 rounded-xl border border-border bg-card p-3.5"
          >
            <div className="flex items-center gap-2.5">
              <Skeleton className="size-[30px] rounded-full" />
              <Skeleton className="h-4 w-20" />
              <span className="flex-1" />
              <Skeleton className="h-5 w-14 rounded-full" />
            </div>
            <Skeleton className="h-3.5 w-40" />
            <Skeleton className="h-[34px] w-full" />
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-[68px] w-full" />
            <div className="flex gap-1.5">
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-8 w-16" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// --- 页面 ---------------------------------------------------------------------

export default function Home() {
  const controlsEnabled = useControlsEnabled();
  const {
    data: identities,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  // 每具身份的读数（年轮/预算/今日）、待办（审批）与活动——共享各页
  // 既有 query key，命中缓存不重复请求；静默失败退化为无读数/无信号。
  const list = identities ?? [];
  const usageQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["usage", it.id],
      queryFn: () => fetchUsage(it.id),
      refetchInterval: USAGE_IDLE_POLL_MS,
    })),
  });
  const approvalQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["approvals", it.id],
      queryFn: () => fetchApprovals(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const activityQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["activity", it.id],
      queryFn: () => fetchActivity(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });

  const slots: SlotData[] = list.map((identity, i) => ({
    identity,
    usage: usageQueries[i]?.data,
    approvals: approvalQueries[i]?.data,
    activity: activityQueries[i]?.data,
  }));
  const signals = slots.flatMap((slot) =>
    signalsFor(slot.identity, slot.usage, slot.approvals, slot.activity)
  );
  const signalsPending =
    slots.length > 0 &&
    (usageQueries.some((q) => q.isPending) ||
      approvalQueries.some((q) => q.isPending) ||
      activityQueries.some((q) => q.isPending));

  if (isLoading) return <HomeSkeleton />;

  const liveCount = list.filter((it) => it.live).length;
  const now = new Date();
  const mmdd = `${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;

  const masthead = (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
      <span className="font-note text-[19px] font-semibold tracking-[0.01em]">
        工作台
      </span>
      {!isError && (
        <span className="text-[13px] text-muted-foreground">
          {list.length} 具心智 · 运行中 {liveCount} · {mmdd}
        </span>
      )}
      <span className="flex-1" />
      <div className="flex flex-wrap items-center gap-1.5">
        {controlsEnabled && <KillAllButton />}
        {list.length > 0 && (
          <AuthenticatedDownload
            variant="ghost"
            size="sm"
            title="导出全部身份"
            url={exportAllUrl()}
            filename="mindloop-identities.tgz"
          >
            <Download className="size-3" />
            导出全部
          </AuthenticatedDownload>
        )}
        {controlsEnabled && <ImportIdentityForm />}
        {controlsEnabled && <NewIdentityForm />}
      </div>
    </div>
  );

  if (isError) {
    return (
      <div className="space-y-3.5">
        {masthead}
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (list.length === 0) {
    return (
      <div className="space-y-3.5">
        {masthead}
        <Empty>
          <EmptyHeader>
            <EmptyTitle>还没有身份</EmptyTitle>
            <EmptyDescription>
              新建一个身份，它就可以开始思考与对话。
            </EmptyDescription>
          </EmptyHeader>
          {controlsEnabled && <NewIdentityForm />}
        </Empty>
      </div>
    );
  }

  return (
    <div className="space-y-3.5">
      {masthead}
      <NeedYou rows={signals} pending={signalsPending} />
      {slots.length <= 6 ? (
        <div className="grid gap-3 min-[880px]:grid-cols-2 min-[1440px]:grid-cols-3">
          {slots.map((slot) => (
            <IdentityCard
              key={slot.identity.id}
              slot={slot}
              controlsEnabled={controlsEnabled}
            />
          ))}
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-border bg-card">
          {slots.map((slot) => (
            <IdentityRow
              key={slot.identity.id}
              slot={slot}
              controlsEnabled={controlsEnabled}
            />
          ))}
        </div>
      )}
      <p className="text-[12px] leading-relaxed text-faint">
        年轮条 = 近 14 天：有柱 = 那天有调用，空段 = 休息。左栏负责切换身份，这里负责读数与待办。
      </p>
    </div>
  );
}
