// 同行面（avatar.md §6.2：工作台重生——第一屏回答"今天我们互相成就
// 了什么"）。结构 = masthead（同行面 · N 具 · 运行中 M + 批量操作 +
// ⌘K）→ 需要你（theater §5 全站唯一告警面，原样保留）→ 同行卡阵
// （每具一张：三轴读数 能力/信任/传承 + 今日账目一句话）→ 共同成长账
// 三栏（它为你 / 你为它 / 一起长出——双向记账，只记真实事件）。
//
// 机制账（avatar.md §3.2）：卡上不出现阶段/百分比这类无机制支撑的
// 拟人指标——三轴全部是既有机制读数（skills/tasks/usage/policy env/
// memories/taste，聚合逻辑在 lib/growth-ledger 配测试）。>6 具降级
// 列表行；数据静默失败退化为「读数缺席」，不打断页面。

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, MoreHorizontal, Play, Plus, Square, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { AuthenticatedDownload } from "~/components/authenticated-download";
import { CommandPalette } from "~/components/command-palette";
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
  fetchIdentityEnv,
  fetchMemories,
  fetchMindlog,
  fetchSkills,
  fetchTaste,
  fetchTasks,
  fetchUsage,
  importIdentities,
  killAll,
} from "~/lib/api";
import {
  axisLines,
  ledgerFor,
  mergeLedgers,
  parsePolicyMode,
  type AxisFacts,
  type IdentityLedger,
  type LedgerEvent,
} from "~/lib/growth-ledger";
import { formatClock, formatRelativeTime } from "~/lib/format";
import { phaseFor } from "~/lib/pet-state";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import type {
  Identity,
  IdentityActivity,
  MemoryInfo,
  Mindlog,
  PendingApproval,
  SkillsView,
  TasteSummary,
  Usage,
  AgentTask,
  IdentityEnv,
} from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 同行面" }];
}

function identityPath(id: string, rest = ""): string {
  return `/i/${encodeURIComponent(id)}${rest}`;
}

// 成长账的轨迹窗口：行数窗口（后端只有行数语义的窗口参数），与
// 泳道首屏同量级。窗口起点落在今日之内时 UI 必须标注（窗口诚实）。
const LEDGER_TAIL = 1000;
const LEDGER_POLL_MS = 60_000;

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
// signalsFor 已升格为全局数据层（theater.md §5 需要你行动条）：实现
// 在 lib/need-you.ts，此处重导出保住回归单测的既有导入路径。

import { signalsFor, type SignalRow } from "~/lib/need-you";

export { signalsFor, type SignalRow };

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

/** ⋯ = 日志 / 导出 / 删除身份。 */
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

// --- 三轴读数（机制账：全部来自既有机制，缺读数就说缺）-----------------------

interface SlotData {
  identity: Identity;
  usage: Usage | undefined;
  approvals: PendingApproval[] | undefined;
  activity: IdentityActivity | undefined;
  tasks: AgentTask[] | undefined;
  skills: SkillsView | undefined;
  memories: MemoryInfo[] | undefined;
  taste: TasteSummary | undefined;
  env: IdentityEnv | undefined;
  mindlog: Mindlog | undefined;
}

export function axisFacts(slot: SlotData): AxisFacts {
  const envVars = slot.env?.env ?? [];
  const findVar = (key: string) =>
    envVars.find((e) => e.key === key)?.value;
  const policy = parsePolicyMode(findVar("MINDLOOP_EXEC_POLICY"));
  const spontRaw = findVar("MINDLOOP_SPONTANEOUS_TOKENS");
  const spontaneous = spontRaw ? Number(spontRaw) : null;

  const admission = slot.usage?.admission;
  // 近 7 天显式委托：完成 / 收尾（succeeded+failed）。在途不算完成率
  // 的分母——分母里塞未决项会把"还没做完"算成"没做成"。
  const weekAgo = Date.now() - 7 * 86_400_000;
  const recent = slot.tasks?.filter((t) => Date.parse(t.created_at) >= weekAgo);

  return {
    skills: slot.skills
      ? slot.skills.skills.filter((s) => s.source === "identity").length
      : null,
    tasksDone: recent ? recent.filter((t) => t.status === "succeeded").length : null,
    tasksTotal: recent
      ? recent.filter((t) => t.status === "succeeded" || t.status === "failed").length
      : null,
    policyMode: policy.mode,
    policyDefaulted: policy.defaulted,
    admission: admission
      ? { used: admission.used_today, limit: admission.daily_limit }
      : null,
    spontaneous:
      spontaneous !== null && Number.isFinite(spontaneous) && spontaneous > 0
        ? spontaneous
        : null,
    memories: slot.memories
      ? slot.memories.filter((m) => !m.status || m.status === "active").length
      : null,
    taste7d: slot.taste
      ? {
          approves: slot.taste.Approves ?? 0,
          denies: slot.taste.Denies ?? 0,
          undoes: slot.taste.Undoes ?? 0,
        }
      : null,
  };
}

/** 卡上的今日一句话：自发 N 件 · 你过手 M 次 · 新记忆 K 条。 */
function todayLine(ledger: IdentityLedger | undefined): string {
  if (!ledger) return "今日账目读数缺席";
  const a = ledger.itForYou.length;
  const b = ledger.youForIt.length;
  const c = ledger.together.length;
  if (a + b + c === 0) return "今日还没有新账";
  const parts: string[] = [];
  if (a > 0) parts.push(`它自己收了 ${a} 件事`);
  if (b > 0) parts.push(`你过手 ${b} 次`);
  if (c > 0) parts.push(`长出 ${c} 条新记忆`);
  return `今日${parts.join(" · ")}`;
}

// --- 同行卡 / 行 -----------------------------------------------------------------

function AxisCell({ label, text }: { label: string; text: string }) {
  return (
    <div className="rounded-lg border border-border px-2.5 py-2">
      <div className="font-mono text-[10px] tracking-[0.08em] text-faint">
        {label}
      </div>
      <div className="mt-0.5 truncate text-[12px] text-muted-foreground" title={text}>
        {text}
      </div>
    </div>
  );
}

function CompanionCard({
  slot,
  controlsEnabled,
}: {
  slot: SlotData;
  controlsEnabled: boolean;
}) {
  const { identity } = slot;
  const facts = useMemo(() => axisFacts(slot), [slot]);
  const axes = useMemo(() => axisLines(facts), [facts]);
  const ledger = useMemo(
    () =>
      slot.mindlog
        ? ledgerFor({
            identityId: identity.id,
            identityName: identity.name,
            steps: slot.mindlog.steps,
            memories: slot.memories,
          })
        : undefined,
    [slot.mindlog, slot.memories, identity.id, identity.name]
  );
  return (
    <div className="flex flex-col gap-2.5 rounded-xl border border-border bg-card p-3.5 pb-3">
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
        <span className="ml-auto flex items-center gap-2">
          <StatusChip live={identity.live} />
        </span>
      </div>
      <div className="-mt-1 text-[12px] text-muted-foreground">
        {activityLine(identity, slot.activity)}
      </div>
      <div className="grid grid-cols-3 gap-2">
        <AxisCell label="能力" text={axes.ability} />
        <AxisCell label="信任" text={axes.trust} />
        <AxisCell label="传承" text={axes.legacy} />
      </div>
      <div className="text-[12.5px] text-muted-foreground">
        {todayLine(ledger)}
      </div>
      {/* 操作行恒为单行：调度钮用图标态（compact）压缩宽度。 */}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button size="sm" asChild>
          <Link to={identityPath(identity.id)}>进舞台 →</Link>
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

function CompanionRow({ slot }: { slot: SlotData }) {
  const { identity } = slot;
  const facts = useMemo(() => axisFacts(slot), [slot]);
  const axes = useMemo(() => axisLines(facts), [facts]);
  const ledger = useMemo(
    () =>
      slot.mindlog
        ? ledgerFor({
            identityId: identity.id,
            identityName: identity.name,
            steps: slot.mindlog.steps,
            memories: slot.memories,
          })
        : undefined,
    [slot.mindlog, slot.memories, identity.id, identity.name]
  );
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
      <span className="hidden max-w-44 shrink-0 truncate text-[11.5px] text-muted-foreground xl:block">
        {todayLine(ledger)}
      </span>
      <span className="hidden w-40 shrink-0 truncate text-right text-[11px] text-faint md:block">
        {axes.trust}
      </span>
      <CardMenu identityId={identity.id} name={identity.name} />
    </div>
  );
}

// --- 共同成长账（三栏双向记账）--------------------------------------------------

const LEDGER_ROW_LIMIT = 6;

function LedgerColumn({
  kicker,
  tone,
  events,
}: {
  kicker: string;
  tone: string;
  events: LedgerEvent[];
}) {
  const shown = events.slice(0, LEDGER_ROW_LIMIT);
  const rest = events.length - shown.length;
  return (
    <div className="min-w-0">
      <div className={cn("mb-1.5 font-mono text-[10.5px] tracking-[0.1em]", tone)}>
        {kicker}
        {events.length > 0 && (
          <span className="ml-1.5 text-faint">{events.length}</span>
        )}
      </div>
      {shown.length === 0 ? (
        <div className="py-1 text-[12.5px] text-faint">今日还没有</div>
      ) : (
        shown.map((ev) => (
          <div
            key={ev.stepId}
            className="flex gap-2 border-b border-border py-1.5 text-[12.5px] last:border-b-0"
          >
            <span className="shrink-0 pt-px font-mono text-[10.5px] text-faint">
              {formatClock(ev.ts).slice(0, 5)}
            </span>
            <span className="shrink-0 pt-px font-mono text-[10.5px] text-muted-foreground">
              {ev.identityName}
            </span>
            <span
              className="min-w-0 flex-1 truncate text-muted-foreground"
              title={ev.summary}
            >
              {ev.summary}
            </span>
          </div>
        ))
      )}
      {rest > 0 && (
        <div className="pt-1 text-[11.5px] text-faint">另有 {rest} 条</div>
      )}
    </div>
  );
}

function GrowthLedgerCard({ ledgers }: { ledgers: IdentityLedger[] }) {
  const merged = useMemo(() => mergeLedgers(ledgers), [ledgers]);
  // 窗口诚实：有任何身份的窗口起点落在今日之内，计数就是"近 N 步内"
  // 的下界——脚注必须说出来，不得写成"今日共 N 件"。
  const partial = ledgers.some((l) => !l.windowCoversToday);
  return (
    <section className="rounded-xl border border-border bg-card">
      <div className="flex flex-wrap items-baseline gap-2 border-b border-border px-4 py-2.5">
        <span className="font-mono text-[10.5px] tracking-[0.1em] text-faint">
          共同成长账
        </span>
        <span className="text-[12px] text-faint">
          互相成就 · 双向记账 · 只记真实事件，无积分无庆祝
        </span>
        <span className="ml-auto text-[11px] text-faint">
          {partial
            ? `窗口 = 各身份 mindlog 最近 ${LEDGER_TAIL} 步（有身份的窗口起点在今日之内，更早的今日事件未含）`
            : "窗口 = 各身份 mindlog 最近 1000 步，覆盖今日"}
        </span>
      </div>
      <div className="grid gap-4 px-4 py-3 min-[880px]:grid-cols-3">
        <LedgerColumn
          kicker="它为你"
          tone="text-moss"
          events={merged.itForYou}
        />
        <LedgerColumn
          kicker="你为它"
          tone="text-lake"
          events={merged.youForIt}
        />
        <LedgerColumn
          kicker="一起长出"
          tone="text-resin"
          events={merged.together}
        />
      </div>
    </section>
  );
}

// 组件导出仅供回归测试（companion-card.test.tsx）——渲染契约钉死：
// 三轴词表、今日账目一句话、三栏顺序与窗口诚实脚注。
export { CompanionCard, GrowthLedgerCard };

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
            <div className="grid grid-cols-3 gap-2">
              <Skeleton className="h-11 w-full rounded-lg" />
              <Skeleton className="h-11 w-full rounded-lg" />
              <Skeleton className="h-11 w-full rounded-lg" />
            </div>
            <Skeleton className="h-3.5 w-52" />
            <div className="flex gap-1.5">
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-8 w-16" />
            </div>
          </div>
        ))}
      </div>
      <Skeleton className="h-40 w-full rounded-xl" />
    </div>
  );
}

// --- 页面 ---------------------------------------------------------------------

export default function Home() {
  const controlsEnabled = useControlsEnabled();
  const [paletteOpen, setPaletteOpen] = useState(false);
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

  // 每具身份的读数与账目——共享各页既有 query key（tasks/skills/
  // memories/taste/env），命中缓存不重复请求；静默失败退化为无读数/
  // 无事件，不打断页面。查询键与既有页面一致是缓存共享的前提。
  const list = identities ?? [];
  const per = <T,>(fn: (it: Identity) => T): T[] => list.map(fn);
  const usageQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["usage", it.id],
      queryFn: () => fetchUsage(it.id),
      refetchInterval: USAGE_IDLE_POLL_MS,
    })),
  });
  const approvalQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["approvals", it.id],
      queryFn: () => fetchApprovals(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const activityQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["activity", it.id],
      queryFn: () => fetchActivity(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const taskQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["tasks", it.id],
      queryFn: () => fetchTasks(it.id),
      refetchInterval: 30_000,
    })),
  });
  const skillQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["skills", it.id],
      queryFn: () => fetchSkills(it.id),
      refetchInterval: 120_000,
    })),
  });
  const memoryQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["memories", it.id],
      queryFn: () => fetchMemories(it.id),
      refetchInterval: LEDGER_POLL_MS,
    })),
  });
  const tasteQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["taste", it.id],
      queryFn: () => fetchTaste(it.id),
      refetchInterval: LEDGER_POLL_MS,
    })),
  });
  const envQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["env", it.id],
      queryFn: () => fetchIdentityEnv(it.id),
      refetchInterval: 300_000,
    })),
  });
  const mindlogQueries = useQueries({
    queries: per((it) => ({
      queryKey: ["ledger-steps", it.id],
      queryFn: () => fetchMindlog(it.id, { tail: LEDGER_TAIL }),
      refetchInterval: LEDGER_POLL_MS,
    })),
  });

  const slots: SlotData[] = list.map((identity, i) => ({
    identity,
    usage: usageQueries[i]?.data,
    approvals: approvalQueries[i]?.data,
    activity: activityQueries[i]?.data,
    tasks: taskQueries[i]?.data,
    skills: skillQueries[i]?.data,
    memories: memoryQueries[i]?.data,
    taste: tasteQueries[i]?.data,
    env: envQueries[i]?.data,
    mindlog: mindlogQueries[i]?.data,
  }));
  const signals = slots.flatMap((slot) =>
    signalsFor(slot.identity, slot.usage, slot.approvals, slot.activity)
  );
  const signalsPending =
    slots.length > 0 &&
    (usageQueries.some((q) => q.isPending) ||
      approvalQueries.some((q) => q.isPending) ||
      activityQueries.some((q) => q.isPending));
  const ledgers = useMemo(
    () =>
      slots.flatMap((slot) =>
        slot.mindlog
          ? [
              ledgerFor({
                identityId: slot.identity.id,
                identityName: slot.identity.name,
                steps: slot.mindlog.steps,
                memories: slot.memories,
              }),
            ]
          : []
      ),
    [slots]
  );

  // ⌘K 一级入口（avatar.md §6.1）：工作台同样有命令面板。
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

  if (isLoading) return <HomeSkeleton />;

  const liveCount = list.filter((it) => it.live).length;
  const now = new Date();
  const mmdd = `${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;

  const masthead = (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
      <span className="font-note text-[19px] font-semibold tracking-[0.01em]">
        同行面
      </span>
      {!isError && (
        <span className="text-[13px] text-muted-foreground">
          {list.length} 具心智 · 运行中 {liveCount} · {mmdd}
        </span>
      )}
      <span className="flex-1" />
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          size="sm"
          variant="outline"
          data-testid="palette-open"
          title="命令面板（Ctrl K）"
          onClick={() => setPaletteOpen(true)}
        >
          ⌘K
        </Button>
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
            <EmptyTitle>还没有同行者</EmptyTitle>
            <EmptyDescription>
              新建一个身份，它就可以开始思考与对话；喂它、教它、委托它，
              看着它长成你的数字分身。
            </EmptyDescription>
          </EmptyHeader>
          {controlsEnabled && <NewIdentityForm />}
        </Empty>
      </div>
    );
  }

  return (
    <div className="space-y-3.5 pt-4">
      {masthead}
      <NeedYou rows={signals} pending={signalsPending} />
      {slots.length <= 6 ? (
        <div className="grid gap-3 min-[880px]:grid-cols-2 min-[1440px]:grid-cols-3">
          {slots.map((slot) => (
            <CompanionCard
              key={slot.identity.id}
              slot={slot}
              controlsEnabled={controlsEnabled}
            />
          ))}
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-border bg-card">
          {slots.map((slot) => (
            <CompanionRow key={slot.identity.id} slot={slot} />
          ))}
        </div>
      )}
      <GrowthLedgerCard ledgers={ledgers} />
      <p className="text-[12px] leading-relaxed text-faint">
        三轴读数全部来自既有机制：能力 = 技能与近 7 天委托；信任 = 执行授权档与预算水位；
        传承 = 在案记忆与你近 7 天的批改。⌘K 是全部面板的一级入口。
      </p>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}
