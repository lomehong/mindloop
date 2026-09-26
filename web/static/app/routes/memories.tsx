import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Search, ShieldOff } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { IdentityTabs } from "~/components/identity-tabs";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { LoadingDots } from "~/components/ui/loading-dots";
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
    : "Untitled memory";
}

function memoryDate(created: string | null, mtime: number) {
  if (created) return formatDateTime(created);
  return new Date(mtime * 1000).toLocaleDateString();
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

  const { data: memories, isLoading } = useQuery({
    queryKey: ["memories", identityId],
    queryFn: () => fetchMemories(identityId),
    refetchInterval: pollWhileLive(live),
  });

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

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs identityId={identityId} live={live} active="memories" />
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
        <div className="flex flex-col gap-4 lg:flex-row">
          <aside className="min-w-0 shrink-0 lg:w-80">
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
            <div className="mb-1 text-xs text-muted-foreground">
              {filtered.length === memories.length
                ? `${memories.length} 条记忆`
                : `${filtered.length} / ${memories.length} 条记忆`}
            </div>
            <div className="max-h-[42vh] overflow-y-auto rounded-lg border lg:max-h-[calc(100vh-13rem)]">
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
                      "block w-full border-b px-3 py-2.5 text-left last:border-b-0 hover:bg-accent",
                      mem.name === active && "bg-accent",
                      !isActive(mem.status) && "opacity-60"
                    )}
                    title={mem.name}
                  >
                    <span className="mb-1 flex items-center gap-2">
                      <Badge
                        variant="outline"
                        className="max-w-28 truncate text-[10px]"
                      >
                        {mem.type}
                      </Badge>
                      {statusLabel(mem.status) && (
                        <Badge
                          variant="outline"
                          className="shrink-0 text-[10px] text-muted-foreground"
                        >
                          {statusLabel(mem.status)}
                        </Badge>
                      )}
                      <span className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground">
                        {memoryDate(mem.created, mem.mtime)}
                      </span>
                    </span>
                    <span className="line-clamp-2 block text-sm font-medium leading-snug">
                      {mem.summary || readableSlug(mem.slug)}
                    </span>
                    {mem.summary && (
                      <span className="mt-1 block truncate font-mono text-[10px] text-muted-foreground">
                        {readableSlug(mem.slug)}
                      </span>
                    )}
                  </button>
                ))
              )}
            </div>
          </aside>
          <div className="min-h-72 min-w-0 flex-1 rounded-lg border bg-card p-4 sm:p-6">
            {!active ? (
              <div className="flex min-h-60 items-center justify-center text-sm text-muted-foreground">
                Choose a different filter to view a memory.
              </div>
            ) : memory ? (
              <>
                {activeInfo && (
                  <div className="mb-5 border-b pb-4">
                    <div className="mb-2 flex flex-wrap items-center gap-2">
                      <Badge variant="secondary">{activeInfo.type}</Badge>
                      {statusLabel(activeInfo.status) && (
                        <Badge
                          variant="outline"
                          className="text-muted-foreground"
                        >
                          {statusLabel(activeInfo.status)}
                        </Badge>
                      )}
                      <span className="text-xs text-muted-foreground">
                        {memoryDate(activeInfo.created, activeInfo.mtime)}
                      </span>
                      {activeInfo.id && (
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {activeInfo.id}
                        </span>
                      )}
                      {!revising && isActive(activeInfo.status) && activeInfo.id && (
                        <span className="ml-auto flex gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={beginRevise}
                          >
                            <Pencil /> 修订
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => setConfirmInvalidate(true)}
                          >
                            <ShieldOff /> 失效
                          </Button>
                        </span>
                      )}
                    </div>
                    <h2 className="text-lg font-semibold leading-snug">
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
                  <Markdown className="max-w-none">{memoryBody(memory.content)}</Markdown>
                )}
              </>
            ) : (
              <LoadingDots />
            )}
          </div>
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
