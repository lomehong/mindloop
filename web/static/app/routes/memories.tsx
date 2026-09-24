import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useMemo, useState } from "react";
import { useParams } from "react-router";

import { IdentityTabs } from "~/components/identity-tabs";
import { Badge } from "~/components/ui/badge";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { LoadingDots } from "~/components/ui/loading-dots";
import { Input } from "~/components/ui/input";
import { Markdown } from "~/components/ui/markdown";
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
  pollWhileLive,
} from "~/lib/api";
import { cn } from "~/lib/utils";

const ALL_TYPES = "__all__";

function readableSlug(slug: string) {
  const readable = slug.replace(/[-_]+/g, " ").trim();
  return readable
    ? readable.charAt(0).toLocaleUpperCase() + readable.slice(1)
    : "Untitled memory";
}

function memoryDate(created: string | null, mtime: number) {
  if (created) return created.slice(0, 16).replace("T", " ");
  return new Date(mtime * 1000).toLocaleDateString();
}

function memoryBody(content: string) {
  const lines = content.split(/\r?\n/);
  if (lines[0]?.trim() !== "---") return content;
  const closing = lines.slice(1).findIndex((line) => line.trim() === "---");
  if (closing < 0) return content;
  return lines.slice(closing + 2).join("\n").replace(/^\s+/, "");
}

export function meta() {
  return [{ title: "Headlong · memories" }];
}

export default function MemoriesPage() {
  const { identityId = "" } = useParams();
  const [selected, setSelected] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState(ALL_TYPES);

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

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl px-4">
      <IdentityTabs identityId={identityId} live={live} active="memories" />
      {!memories || memories.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No memories</EmptyTitle>
            <EmptyDescription>
              This identity's memories/ directory is empty.
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
                  placeholder="Search memories"
                  aria-label="Search memories"
                  className="pl-8"
                />
              </div>
              <Select value={typeFilter} onValueChange={setTypeFilter}>
                <SelectTrigger size="sm" className="max-w-36">
                  <SelectValue placeholder="All types" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL_TYPES}>
                    All types ({memories.length})
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
                ? `${memories.length} memories`
                : `${filtered.length} of ${memories.length} memories`}
            </div>
            <div className="max-h-[42vh] overflow-y-auto rounded-lg border lg:max-h-[calc(100vh-13rem)]">
              {filtered.length === 0 ? (
                <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                  No memories match these filters.
                </div>
              ) : (
                filtered.map((mem) => (
                  <button
                    key={mem.name}
                    type="button"
                    onClick={() => setSelected(mem.name)}
                    className={cn(
                      "block w-full border-b px-3 py-2.5 text-left last:border-b-0 hover:bg-accent",
                      mem.name === active && "bg-accent"
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
                      <span className="text-xs text-muted-foreground">
                        {memoryDate(activeInfo.created, activeInfo.mtime)}
                      </span>
                      {activeInfo.id && (
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {activeInfo.id}
                        </span>
                      )}
                    </div>
                    <h2 className="text-lg font-semibold leading-snug">
                      {activeInfo.summary || readableSlug(activeInfo.slug)}
                    </h2>
                  </div>
                )}
                <Markdown className="max-w-none">{memoryBody(memory.content)}</Markdown>
              </>
            ) : (
              <LoadingDots />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
