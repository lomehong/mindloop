import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Search, ShieldOff } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import { Input } from "~/components/ui/input";
import { Markdown } from "~/components/ui/markdown";
import { Textarea } from "~/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import {
  fetchIdentityStatus,
  fetchMemories,
    fetchMemRecall,
  fetchMemory,
  invalidateMemory,
  pollWhileLive,
  reviseMemory,
} from "~/lib/api";
import { formatDateTime } from "~/lib/format";
import { cn } from "~/lib/utils";

const ALL_TYPES = "__all__";

function readableSlug(slug: string) {
  const readable = slug.replace(/[-_]+/g, " ").trim();
  return readable
    ? readable.charAt(0).toLocaleUpperCase() + readable.slice(1)
    : "未命名记忆";
}

function memoryDate(created: string | null, mtime: number) {
  if (created) return formatDateTime(created);
  return new Date(mtime * 1000).toLocaleDateString();
}

/** 列表行的紧凑日期：MM-DD，跨年才带年份（详情页仍用完整日期）。 */
function memoryDateShort(created: string | null, mtime: number) {
  const full = memoryDate(created, mtime);
  const match = full.match(/^(\d{4})-(\d{2})-(\d{2})/);
  if (!match) return full;
  const [, year, month, day] = match;
  return year === String(new Date().getFullYear())
    ? `${month}-${day}`
    : `${year}-${month}-${day}`;
}

function memoryBody(content: string) {
  const lines = content.split(/\r?\n/);
  if (lines[0]?.trim() !== "---") return content;
  const closing = lines.slice(1).findIndex((line) => line.trim() === "---");
  if (closing < 0) return content;
  return lines.slice(closing + 2).join("\n").replace(/^\s+/, "");
}

// 记忆状态：空串与 "active" 都算活动；invalid/superseded 已退出
// 检索与显式操作（后端只在非活动时写入 status 字段）。
function isActive(status?: string) {
  return !status || status === "active";
}

function statusLabel(status?: string) {
  if (status === "superseded") return "已被替代";
  if (status === "invalid") return "已失效";
  return null;
}

export function meta() {
  return [{ title: "mindloop · 记忆" }];
}

export default function MemoriesPage() {
  const { identityId = "" } = useParams();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState(ALL_TYPES);
  // 修订编辑态：draft 是正文（frontmatter 之上的 Markdown）；
  // reviseTarget 绑定条目名——切走条目自动退出编辑（草稿不跨越）。
  const [reviseTarget, setReviseTarget] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [confirmInvalidate, setConfirmInvalidate] = useState(false);

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 2000,
  });
  const live = status?.live ?? false;

  const {
    data: memories,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["memories", identityId],
    queryFn: () => fetchMemories(identityId),
    refetchInterval: pollWhileLive(live),
  });

  const { data: recall } = useQuery({
    queryKey: ["mem-recall", identityId],
    queryFn: () => fetchMemRecall(identityId, 20),
    refetchInterval: pollWhileLive(live),
  });

  const recallStats = useMemo(() => {
    const rows = recall ?? [];
    if (rows.length === 0) return null;
    const totalHits = rows.reduce((a, r) => a + r.hit_count, 0);
    const injected = rows.filter((r) => r.injected > 0).length;
    const skipped = rows.filter((r) => r.skipped).length;
    const avgMs =
      rows.reduce((a, r) => a + (r.elapsed_ms ?? 0), 0) / rows.length;
    return { total: rows.length, totalHits, injected, skipped, avgMs };
  }, [recall]);

  const types = useMemo(() => {
    const counts = new Map<string, number>();
    for (const memory of memories ?? []) {
      counts.set(memory.type, (counts.get(memory.type) ?? 0) + 1);
    }
    return [...counts.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [memories]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return (memories ?? []).filter((memory) => {
      if (typeFilter !== ALL_TYPES && memory.type !== typeFilter) return false;
      if (!needle) return true;
      return [memory.summary, memory.slug, memory.type, memory.name]
        .filter(Boolean)
        .some((value) => value?.toLocaleLowerCase().includes(needle));
    });
  }, [memories, query, typeFilter]);

  const active =
    (selected && filtered.some((memory) => memory.name === selected) && selected) ||
    filtered[0]?.name ||
    null;
  const activeInfo = memories?.find((item) => item.name === active);

  const { data: memory } = useQuery({
    queryKey: ["memory", identityId, active],
    queryFn: () => fetchMemory(identityId, active as string),
    enabled: !!active,
  });

  // 切换条目退出编辑态——草稿属于当前条目，不能跨越。
  const revising = reviseTarget !== null && reviseTarget === active;

  const revise = useMutation({
    mutationFn: (memId: string) => reviseMemory(identityId, memId, draft),
    onSuccess: (result) => {
      toast.success(`已修订为新版本 ${result.id}`);
      setReviseTarget(null);
      queryClient.invalidateQueries({ queryKey: ["memories", identityId] });
      queryClient.invalidateQueries({ queryKey: ["memory", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const invalidate = useMutation({
    mutationFn: (memId: string) => invalidateMemory(identityId, memId),
    onSuccess: () => {
      toast.success("已失效——退出检索，文件保留供审计");
      queryClient.invalidateQueries({ queryKey: ["memories", identityId] });
      queryClient.invalidateQueries({ queryKey: ["memory", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const beginRevise = () => {
    if (!memory || !active) return;
    setDraft(memoryBody(memory.content).trim());
    setReviseTarget(active);
  };

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="mx-auto flex w-full max-w-7xl 2xl:max-w-[1600px] flex-col gap-4 lg:flex-row">
        <div className="shrink-0 space-y-2 lg:w-80">
          <Skeleton className="h-9 w-full" />
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
        <div className="min-w-0 flex-1 space-y-3 lg:pl-6">
          <Skeleton className="h-4 w-52" />
          <Skeleton className="h-6 w-72" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
          <Skeleton className="h-4 w-2/3" />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
      {!memories || memories.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无记忆</EmptyTitle>
            <EmptyDescription>
              此身份的 memories/ 目录为空。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="flex flex-col gap-4 lg:flex-row lg:gap-0 xl:gap-6">
          <aside className="min-w-0 shrink-0 lg:w-80 lg:border-r lg:border-line lg:pr-4">
            <div className="mb-2 flex gap-2">
              <div className="relative min-w-0 flex-1">
                <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="搜索记忆"
                  aria-label="搜索记忆"
                  className="pl-8"
                />
              </div>
              <Select value={typeFilter} onValueChange={setTypeFilter}>
                <SelectTrigger size="sm" className="max-w-36">
                  <SelectValue placeholder="全部类型" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL_TYPES}>
                    全部类型 ({memories.length})
                  </SelectItem>
                  {types.map(([type, count]) => (
                    <SelectItem key={type} value={type}>
                      {type} ({count})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="mb-1 px-1 font-mono text-[10.5px] tracking-wide text-faint">
              {filtered.length === memories.length
                ? `${memories.length} 条记忆`
                : `${filtered.length} / ${memories.length} 条记忆`}
            </div>
            <div className="max-h-[42vh] divide-y divide-line overflow-y-auto border-y border-line lg:max-h-[calc(100vh-13rem)]">
              {filtered.length === 0 ? (
                <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                  没有记忆符合这些筛选条件。
                </div>
              ) : (
                filtered.map((mem) => (
                  <button
                    key={mem.name}
                    type="button"
                    onClick={() => setSelected(mem.name)}
                    className={cn(
                      "block w-full px-3 py-2.5 text-left hover:bg-secondary",
                      mem.name === active &&
                        "bg-primary/[0.07] shadow-[inset_2px_0_0_var(--primary)]",
                      !isActive(mem.status) && "opacity-50"
                    )}
                    title={mem.name}
                  >
                    <span className="mb-1 flex items-center gap-2">
                      <Badge
                        variant="outline"
                        className="max-w-28 truncate rounded-full border-line-strong font-mono text-[9.5px] font-normal text-muted-foreground"
                      >
                        {mem.type}
                      </Badge>
                      {statusLabel(mem.status) && (
                        <Badge
                          variant="outline"
                          className="shrink-0 rounded-full border-clay/35 font-mono text-[9.5px] font-normal text-clay"
                        >
                          {statusLabel(mem.status)}
                        </Badge>
                      )}
                      <span className="ml-auto shrink-0 font-mono text-[10px] tabular-nums text-faint">
                        {memoryDateShort(mem.created, mem.mtime)}
                      </span>
                    </span>
                    <span className="line-clamp-2 block font-note text-[13px] leading-snug">
                      {mem.summary || readableSlug(mem.slug)}
                    </span>
                    {mem.summary && (
                      <span className="mt-1 block truncate font-mono text-[10px] text-faint">
                        {readableSlug(mem.slug)}
                      </span>
                    )}
                  </button>
                ))
              )}
            </div>
          </aside>
          <div className="min-h-72 min-w-0 flex-1 lg:pl-6">
            {!active ? (
              <div className="flex min-h-60 items-center justify-center text-sm text-muted-foreground">
                换一个筛选条件以查看记忆。
              </div>
            ) : memory ? (
              <>
                {activeInfo && (
                  <div className="mb-4">
                    <div className="mb-3 flex flex-wrap items-center gap-2 font-mono text-[11px] text-faint">
                      <Badge
                        variant="outline"
                        className="rounded-full border-line-strong font-mono text-[9.5px] font-normal text-muted-foreground"
                      >
                        {activeInfo.type}
                      </Badge>
                      {statusLabel(activeInfo.status) && (
                        <Badge
                          variant="outline"
                          className="rounded-full border-clay/35 font-mono text-[9.5px] font-normal text-clay"
                        >
                          {statusLabel(activeInfo.status)}
                        </Badge>
                      )}
                      <span>{memoryDate(activeInfo.created, activeInfo.mtime)}</span>
                      {activeInfo.id && <span>{activeInfo.id}</span>}
                      {!revising && isActive(activeInfo.status) && activeInfo.id && (
                        <span className="ml-auto flex gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 rounded-lg border-line font-mono text-[11px]"
                            onClick={beginRevise}
                          >
                            <Pencil className="size-3" /> 修订
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 rounded-lg border-clay/40 font-mono text-[11px] text-clay hover:bg-clay/10 hover:text-clay"
                            onClick={() => setConfirmInvalidate(true)}
                          >
                            <ShieldOff className="size-3" /> 失效
                          </Button>
                        </span>
                      )}
                    </div>
                    <h2 className="font-note text-[19px] font-semibold leading-snug">
                      {activeInfo.summary || readableSlug(activeInfo.slug)}
                    </h2>
                  </div>
                )}
                {revising ? (
                  <div className="space-y-3">
                    <Textarea
                      aria-label="修订内容"
                      value={draft}
                      onChange={(event) => setDraft(event.target.value)}
                      rows={14}
                      className="min-h-60 font-mono text-sm leading-relaxed"
                    />
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        onClick={() =>
                          activeInfo?.id && revise.mutate(activeInfo.id)
                        }
                        disabled={revise.isPending || !draft.trim()}
                      >
                        保存修订
                      </Button>
                      <Button variant="ghost" onClick={() => setReviseTarget(null)}>
                        取消
                      </Button>
                      <span className="text-xs text-muted-foreground">
                        旧版本保留在盘上并标记为“已被替代”；检索只命中新版本。
                      </span>
                    </div>
                  </div>
                ) : (
                  <Markdown
                    className="max-w-[640px] border-0 bg-transparent p-0"
                    proseClassName="font-note text-[14px] leading-[1.85]"
                  >
                    {memoryBody(memory.content)}
                  </Markdown>
                )}
              </>
            ) : (
              <div className="space-y-3">
                <Skeleton className="h-4 w-52" />
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-5/6" />
              </div>
            )}
          </div>
          {/* 样张 §5 右栏：它此刻想起什么（mem-recall 台账）——召回是
              观测面不是事实源，展示不改变行为 */}
          <aside className="min-w-0 shrink-0 space-y-3 xl:w-72">
            <div className="rounded-xl border border-border bg-card px-4 py-3">
              <div className="text-[13px] font-medium">它此刻想起什么</div>
              <div className="mb-2 font-mono text-[10px] text-faint">召回台账 · mem-recall</div>
              {(recall ?? []).length === 0 ? (
                <div className="py-1 text-[12px] text-faint">还没有召回记录。</div>
              ) : (
                <div className="flex flex-col">
                  {(recall ?? []).slice(0, 8).map((r, i) => (
                    <div
                      key={`${r.ts}-${i}`}
                      className="flex items-baseline justify-between gap-2 border-b border-line py-1.5 text-[12px] last:border-b-0"
                    >
                      <span className="min-w-0 flex-1 truncate text-muted-foreground">
                        {r.source === "monolith" ? "唤醒上下文" : "对话回复"}
                        {r.injected > 0 ? ` · 注入 ${r.injected} 条` : r.skipped ? " · 跳过" : ""}
                      </span>
                      <span className="shrink-0 font-mono text-[10px] text-faint">
                        hit {r.hit_count}
                      </span>
                    </div>
                  ))}
                </div>
              )}
              {recallStats && (
                <div className="mt-2 border-t border-line pt-2 font-mono text-[10px] leading-relaxed text-faint">
                  最近 {recallStats.total} 次检索 · 命中合计 {recallStats.totalHits} ·
                  注入 {recallStats.injected} · 跳过 {recallStats.skipped} ·
                  均耗 {recallStats.avgMs >= 1 ? `${Math.round(recallStats.avgMs)}ms` : "<1ms"}
                </div>
              )}
            </div>
            <div className="rounded-xl border border-border bg-surface-2 px-4 py-3">
              <div className="font-mono text-[10px] tracking-[0.08em] text-faint">消化链</div>
              <div className="mt-1.5 text-[12px] leading-relaxed text-muted-foreground">
                注意力 S0 沉淀 → 睡眠整理（合并/裁决/降权，未核实标签）→
                手账条目。未核实的沉淀不参与显著性升级——回声室断环。
              </div>
            </div>
          </aside>
        </div>
      )}
      <ConfirmDialog
        open={confirmInvalidate}
        onOpenChange={setConfirmInvalidate}
        tone="danger"
        title="失效这条记忆？"
        description="失效后它退出检索与引用（模型不再看到），文件保留在盘上供审计。此操作不可撤销。"
        confirmText="确认失效"
        onConfirm={() => {
          if (activeInfo?.id) invalidate.mutate(activeInfo.id);
        }}
      />
    </div>
  );
}
