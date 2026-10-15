// 同行面（avatar.md §6.2 + 样张 avatar-workbench.html 对齐，2026-10-15
// 用户拍板视觉以样张为准）：自持整窗、max-w-1200 居中、无侧栏。
// 结构 = masthead（creature + 同行面 + kicker + 状态 chip/⌘K/星系远景/
// 进入舞台）→ 需要你（琥珀条）→ 同行卡阵（阶段 chip + 三轴量条 +
// 今日一句 + 进舞台/成长档案/翻日记）→ 共同成长账三栏。批量操作收进
// masthead 管理菜单。底部体征 pill 常驻（vitals-band）。
//
// 机制账（avatar.md §3.2/§10.1）：阶段 chip 只给投影段名不给百分比；
// 三轴量条用真实读数填充（7 天完成率/授权档投影/在案记忆厚度）；
// 成长账行数窗口起点在今日之内时脚注必须标注。

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Play, Plus, Square, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { AuthenticatedDownload } from "~/components/authenticated-download";
import { CommandPalette } from "~/components/command-palette";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { CreatureSvg } from "~/components/pet/creature";
import { LedgerColumn } from "~/components/ledger-column";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { VitalsBand } from "~/components/vitals-band";
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
  ledgerFor,
  mergeLedgers,
  parsePolicyMode,
  type IdentityLedger,
} from "~/lib/growth-ledger";
import {
  axisMeters,
  phaseChipOf,
  type PolicyMode,
} from "~/lib/growth-dossier";
import { phaseFor } from "~/lib/pet-state";
import { STATUS_BACKGROUND_POLL_MS, USAGE_IDLE_POLL_MS } from "~/lib/polling";
import type {
  AgentTask,
  Identity,
  IdentityActivity,
  IdentityEnv,
  MemoryInfo,
  Mindlog,
  PendingApproval,
  SkillsView,
  TasteSummary,
  Usage,
} from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 同行面" }];
}

function identityPath(id: string, rest = ""): string {
  return `/i/${encodeURIComponent(id)}${rest}`;
}

// 成长账的轨迹窗口：行数窗口（与泳道首屏同量级）；窗口起点落在今日
// 之内时 UI 必须标注（窗口诚实）。
const LEDGER_TAIL = 1000;
const LEDGER_POLL_MS = 60_000;

// --- 批量操作（masthead 管理菜单） ---------------------------------------------

function NewIdentityForm({ inline }: { inline?: boolean }) {
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

  if (inline && !open) {
    return (
      <button
        type="button"
        className="block w-full rounded-md px-2.5 py-1.5 text-left text-xs hover:bg-accent"
        onClick={() => setOpen(true)}
      >
        新建身份
      </button>
    );
  }
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

function ImportIdentityForm({ inline }: { inline?: boolean }) {
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

  if (inline && !open) {
    return (
      <button
        type="button"
        className="block w-full rounded-md px-2.5 py-1.5 text-left text-xs hover:bg-accent"
        onClick={() => setOpen(true)}
      >
        导入身份
      </button>
    );
  }
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

function KillAllButton({ inline }: { inline?: boolean }) {
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

  if (inline) {
    return (
      <>
        <button
          type="button"
          className="block w-full rounded-md px-2.5 py-1.5 text-left text-xs text-clay hover:bg-clay/10"
          onClick={() => mutation.mutate(true)}
        >
          全部停止…
        </button>
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

/** masthead 管理菜单：新建 / 导入 / 导出全部 / 全部停止。 */
function ManageMenu({ controlsEnabled }: { controlsEnabled: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="relative">
      <Button
        variant="outline"
        size="icon-sm"
        title="管理：新建 / 导入 / 导出 / 全部停止"
        aria-label="管理操作"
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
          <div className="absolute right-0 top-full z-40 mt-1 min-w-32 rounded-lg border border-border bg-popover p-1 shadow-md">
            {controlsEnabled && <NewIdentityForm inline />}
            {controlsEnabled && <ImportIdentityForm inline />}
            <div className="[&>span]:block">
              <AuthenticatedDownload
                url={exportAllUrl()}
                filename="mindloop-identities.tgz"
                variant="ghost"
                size="sm"
                className="h-auto w-full justify-start rounded-md px-2.5 py-1.5 text-xs font-normal"
              >
                导出全部
              </AuthenticatedDownload>
            </div>
            {controlsEnabled && <KillAllButton inline />}
          </div>
        </>
      )}
    </div>
  );
}

// --- 信号（需要你，琥珀条形态）-------------------------------------------------
// signalsFor 已升格为全局数据层（theater.md §5 需要你行动条）：实现
// 在 lib/need-you.ts，此处重导出保住回归单测的既有导入路径。

import { signalsFor, type SignalRow } from "~/lib/need-you";

export { signalsFor, type SignalRow };

function NeedYou({ rows, pending }: { rows: SignalRow[]; pending: boolean }) {
  if (pending) {
    return (
      <div className="flex items-center gap-3 rounded-xl border border-line-strong bg-card px-4 py-3">
        <span className="size-2 rounded-full bg-faint" />
        <span className="text-[13px] text-muted-foreground">正在汇总待办…</span>
      </div>
    );
  }
  if (rows.length === 0) {
    return null; // 样张形态：没有待办就不占一行（安静）
  }
  return (
    <section className="overflow-hidden rounded-xl border border-resin/45 bg-resin/5">
      <div className="flex items-center gap-3 px-4 py-3">
        <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-resin/50 px-2.5 py-0.5 font-mono text-[11px] text-resin">
          <span className="size-[5px] animate-pulse rounded-full bg-resin" />
          需要你 · {rows.length} 项
        </span>
        <span className="min-w-0 flex-1 truncate text-[13px] text-muted-foreground">
          {rows.map((r) => `${r.name}：${r.text}`).join(" · ")}
        </span>
        <Link
          to={rows[0].to}
          className="shrink-0 rounded-lg border border-primary/50 px-3.5 py-1.5 text-[13px] text-primary transition-colors hover:bg-primary/10"
        >
          去处理 →
        </Link>
      </div>
      {rows.length > 1 && (
        <div className="border-t border-resin/25">
          {rows.slice(1).map((row) => (
            <Link
              key={row.key}
              to={row.to}
              className="grid grid-cols-[96px_1fr_auto] items-center gap-2.5 px-4 py-2 hover:bg-muted"
            >
              <span className="truncate text-[12.5px] font-medium">{row.name}</span>
              <span className="truncate text-[12px] text-muted-foreground">{row.text}</span>
              <span className="font-mono text-[10.5px] text-resin">去处理 →</span>
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}

// --- 卡内零件 -----------------------------------------------------------------

/** 停止/启动该身份的调度器（names=[] 同旧名册语义）；Shift+点击 = 强制停止。 */
function DispatcherControl({
  identityId,
  running,
}: {
  identityId: string;
  running: boolean;
}) {
  const mutation = useThinkerMutation(identityId);
  const [confirmStop, setConfirmStop] = useState<{ force: boolean } | null>(null);

  if (running) {
    return (
      <>
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
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      disabled={mutation.isPending}
      title="启动全部思考者"
      onClick={() => mutation.mutate({ action: "start", names: [] })}
    >
      <Play className="size-4" />
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

// --- 三轴与今日一句 -----------------------------------------------------------

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

export function axisFacts(slot: SlotData): {
  tasksDone: number | null;
  tasksTotal: number | null;
  policyMode: PolicyMode | null;
  memories: number | null;
} {
  const envVars: Record<string, string> = {};
  for (const e of slot.env?.env ?? []) envVars[e.key] = e.value;
  const policy = parsePolicyMode(envVars["MINDLOOP_EXEC_POLICY"]);
  // 近 7 天显式委托：完成 / 收尾（succeeded+failed）。在途不算完成率
  // 的分母——分母里塞未决项会把"还没做完"算成"没做成"。
  const weekAgo = Date.now() - 7 * 86_400_000;
  const recent = slot.tasks?.filter((t) => Date.parse(t.created_at) >= weekAgo);
  return {
    tasksDone: recent ? recent.filter((t) => t.status === "succeeded").length : null,
    tasksTotal: recent
      ? recent.filter((t) => t.status === "succeeded" || t.status === "failed").length
      : null,
    policyMode: policy.mode,
    memories: slot.memories
      ? slot.memories.filter((m) => !m.status || m.status === "active").length
      : null,
  };
}

/** 卡上的今日一句（样张的叙述行）：自发 N 件 · 你过手 M 次 · 新记忆 K 条。 */
export function todayLine(ledger: IdentityLedger | undefined): string {
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

const METER_TONE: Record<"moss" | "lake" | "resin", string> = {
  moss: "bg-moss",
  lake: "bg-lake",
  resin: "bg-resin",
};

// --- 同行卡 -----------------------------------------------------------------

function CompanionCard({
  slot,
  controlsEnabled,
}: {
  slot: SlotData;
  controlsEnabled: boolean;
}) {
  const { identity } = slot;
  const facts = useMemo(() => axisFacts(slot), [slot]);
  const meters = useMemo(() => axisMeters(facts), [facts]);
  const phase = facts.policyMode ? phaseChipOf(facts.policyMode) : null;
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
    <div className="flex flex-col gap-2.5 rounded-xl border border-line-strong bg-card p-4">
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1.5 rounded-full border border-line-strong px-2.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          <span
            className={cn(
              "size-[5px] rounded-full",
              identity.live ? "animate-pulse bg-primary" : "bg-faint"
            )}
          />
          {identity.name}
        </span>
        {phase && (
          <span className="rounded-full border border-lake/45 px-2.5 py-0.5 font-mono text-[11px] text-lake">
            {phase}
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          {controlsEnabled && identity.thinkers_total > 0 && (
            <DispatcherControl
              identityId={identity.id}
              running={identity.dispatcher?.running ?? false}
            />
          )}
          <CardMenu identityId={identity.id} name={identity.name} />
        </span>
      </div>
      <div className="grid grid-cols-3 gap-2">
        {meters.map((m) => (
          <div key={m.label} className="rounded-lg border border-line px-2.5 py-2">
            <div className="font-mono text-[10px] tracking-[0.08em] text-faint">
              {m.label}
            </div>
            <div className="mt-0.5 truncate text-[12.5px]" title={m.text}>
              {m.text}
            </div>
            <div className="mt-2 h-[4px] overflow-hidden rounded-full bg-surface-2">
              <i
                className={cn("block h-full rounded-full", METER_TONE[m.tone], m.ratio === null && "opacity-20")}
                style={{ width: `${m.ratio === null ? 0 : Math.round(m.ratio * 100)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
      <div className="text-[12.5px] text-muted-foreground">
        {todayLine(ledger)}
        {ledger && !ledger.windowCoversToday && (
          <span
            className="ml-1.5 cursor-default font-mono text-[10px] text-faint"
            title="统计窗口是 mindlog 最近 1000 步，起点落在今日之内——今日更早的事件可能未含"
          >
            近千步内
          </span>
        )}
      </div>
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button size="sm" asChild>
          <Link to={identityPath(identity.id)}>进舞台 →</Link>
        </Button>
        <Button size="sm" variant="outline" asChild>
          <Link to={identityPath(identity.id, "/growth")}>成长档案</Link>
        </Button>
        <Button size="sm" variant="outline" asChild>
          <Link to={identityPath(identity.id, "/diary")}>翻日记</Link>
        </Button>
      </div>
    </div>
  );
}

// --- 共同成长账（三栏双向记账）--------------------------------------------------
// 单列组件在 components/ledger-column（成长档案页共用）。

function GrowthLedgerCard({ ledgers }: { ledgers: IdentityLedger[] }) {
  const merged = useMemo(() => mergeLedgers(ledgers), [ledgers]);
  // 窗口诚实：有任何身份的窗口起点落在今日之内，计数就是"近 N 步内"
  // 的下界——脚注必须说出来，不得写成"今日共 N 件"。
  const partial = ledgers.some((l) => !l.windowCoversToday);
  return (
    <section className="rounded-xl border border-line-strong bg-card">
      <div className="flex flex-wrap items-baseline gap-2 border-b border-border px-4 py-3">
        <span className="text-[14.5px] font-medium">共同成长账</span>
        <span className="rounded border border-line-strong px-1.5 font-mono text-[10px] text-faint">
          互相成就 · 双向记账 · 只记真实事件，无积分无庆祝
        </span>
        <span className="ml-auto text-[11px] text-faint">
          {partial
            ? `窗口 = 各身份 mindlog 最近 ${LEDGER_TAIL} 步（有身份的窗口起点在今日之内，更早的今日事件未含）`
            : "窗口 = 各身份 mindlog 最近 1000 步，覆盖今日"}
        </span>
      </div>
      <div className="grid gap-4 px-4 py-3 min-[880px]:grid-cols-3">
        <LedgerColumn kicker="它为你" tone="text-primary" events={merged.itForYou} />
        <LedgerColumn kicker="你为它" tone="text-lake" events={merged.youForIt} />
        <LedgerColumn kicker="一起长出" tone="text-resin" events={merged.together} />
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
    <div className="mx-auto w-full max-w-[1200px] px-7 py-6">
      <div className="flex items-center gap-3">
        <Skeleton className="size-[34px] rounded-full" />
        <Skeleton className="h-6 w-24" />
        <Skeleton className="h-4 w-52" />
        <span className="flex-1" />
        <Skeleton className="h-8 w-20" />
        <Skeleton className="h-8 w-24" />
        <Skeleton className="h-8 w-32" />
      </div>
      <Skeleton className="mt-4 h-12 w-full rounded-xl" />
      <div className="mt-4 grid gap-3 min-[880px]:grid-cols-2 min-[1280px]:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex flex-col gap-2.5 rounded-xl border border-border bg-card p-4">
            <div className="flex items-center gap-2">
              <Skeleton className="h-5 w-20 rounded-full" />
              <Skeleton className="h-5 w-24 rounded-full" />
            </div>
            <div className="grid grid-cols-3 gap-2">
              <Skeleton className="h-[72px] rounded-lg" />
              <Skeleton className="h-[72px] rounded-lg" />
              <Skeleton className="h-[72px] rounded-lg" />
            </div>
            <Skeleton className="h-3.5 w-52" />
            <div className="flex gap-1.5">
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-8 w-20" />
              <Skeleton className="h-8 w-20" />
            </div>
          </div>
        ))}
      </div>
      <Skeleton className="mt-4 h-44 w-full rounded-xl" />
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
  // 无事件，不打断页面。
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

  // ⌘K 一级入口（avatar.md §6.1）。
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

  if (isLoading) {
    return (
      <div className="h-screen overflow-y-auto bg-background">
        <HomeSkeleton />
      </div>
    );
  }

  const liveCount = list.filter((it) => it.live).length;
  const featured = list.find((it) => it.live) ?? list[0];
  const now = new Date();
  const mmdd = `${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;

  const masthead = (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <CreatureSvg size={34} phase={phaseFor(featured?.id ?? "mindloop")} />
      <div>
        <div className="text-[20px] font-medium leading-tight">同行面</div>
        <div className="font-mono text-[11.5px] text-faint">
          {!isError && `${list.length} 具心智 · 运行中 ${liveCount} · ${mmdd} · 共同成长，互相成就`}
        </div>
      </div>
      <span className="flex-1" />
      <div className="flex flex-wrap items-center gap-1.5">
        {featured && (
          <span className="inline-flex items-center gap-1.5 rounded-full border border-line-strong px-2.5 py-1 font-mono text-[11px] text-muted-foreground">
            <span
              className={cn(
                "size-[5px] rounded-full",
                featured.live ? "animate-pulse bg-primary" : "bg-faint"
              )}
            />
            {featured.name} {featured.live ? "运行中" : "已停"}
          </span>
        )}
        <Button
          size="sm"
          variant="outline"
          data-testid="palette-open"
          title="命令面板（Ctrl K）"
          onClick={() => setPaletteOpen(true)}
        >
          ⌘K
        </Button>
        <Button size="sm" variant="outline" asChild>
          <Link to="/galaxy">星系远景</Link>
        </Button>
        {featured && (
          <Button size="sm" asChild>
            <Link to={identityPath(featured.id)}>进入 {featured.name} 的舞台 →</Link>
          </Button>
        )}
        <ManageMenu controlsEnabled={controlsEnabled} />
      </div>
    </div>
  );

  if (isError) {
    return (
      <div className="h-screen overflow-y-auto bg-background">
        <div className="mx-auto w-full max-w-[1200px] space-y-3.5 px-7 py-6">
          {masthead}
          <QueryErrorBanner error={error} onRetry={() => void refetch()} />
        </div>
      </div>
    );
  }

  if (list.length === 0) {
    return (
      <div className="h-screen overflow-y-auto bg-background">
        <div className="mx-auto w-full max-w-[1200px] space-y-3.5 px-7 py-6">
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
      </div>
    );
  }

  return (
    <div className="h-screen overflow-y-auto bg-background">
      <div className="mx-auto w-full max-w-[1200px] px-7 pb-24 pt-5">
        {masthead}
        <div className="mt-4">
          <NeedYou rows={signals} pending={signalsPending} />
        </div>
        <div className="mt-4 grid gap-3 min-[880px]:grid-cols-2 min-[1280px]:grid-cols-3">
          {slots.map((slot) => (
            <CompanionCard
              key={slot.identity.id}
              slot={slot}
              controlsEnabled={controlsEnabled}
            />
          ))}
        </div>
        <div className="mt-4">
          <GrowthLedgerCard ledgers={ledgers} />
        </div>
        <p className="mt-3 text-[11.5px] leading-relaxed text-faint">
          三轴量条用真实读数填充：能力 = 近 7 天委托完成率；信任 = 执行授权档投影（规则公开）；
          传承 = 在案记忆相对厚度。⌘K 是全部面板的一级入口。
        </p>
      </div>
      {featured && <VitalsBand identityId={featured.id} live={featured.live} />}
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}
