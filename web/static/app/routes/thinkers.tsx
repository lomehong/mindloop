import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DownloadCloud, Zap } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { QueryErrorBanner } from "~/components/query-error-banner";
import {
  StartStopButtons,
  useControlsEnabled,
  useThinkerMutation,
} from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "~/components/ui/tabs";
import {
  fetchDispatch,
  fetchIdentityStatus,
  fetchLog,
  fetchLogs,
  fetchThinkers,
  fetchThinkerSync,
  pollWhileLive,
  pullThinkerSync,
  setThinkerEnabled,
} from "~/lib/api";
import type {
  ThinkerInfo,
  ThinkerState,
  ThinkerSyncEntry,
} from "~/lib/types";
import { formatBytes, formatRelativeTime } from "~/lib/format";
import {
  STATUS_ACTIVE_POLL_MS,
  THINKERS_FEED_POLL_MS,
} from "~/lib/polling";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 思考者" }];
}

// 状态芯片统一（§7 C）：运行=叶绿(primary)呼吸 / 空闲·停止=灰 / 排空=树脂警。
const STATE_STYLES: Record<ThinkerState, string> = {
  stopped:
    "rounded-full border-line bg-secondary font-mono text-[10.5px] font-normal text-muted-foreground",
  idle: "rounded-full border-line bg-secondary font-mono text-[10.5px] font-normal text-muted-foreground",
  active:
    "rounded-full border-primary/30 bg-primary/12 font-mono text-[10.5px] font-normal text-foreground",
  running:
    "rounded-full border-primary/30 bg-primary/12 font-mono text-[10.5px] font-normal text-foreground",
  draining:
    "rounded-full border-resin/40 bg-resin/8 font-mono text-[10.5px] font-normal text-resin",
  disabled:
    "rounded-full border-dashed border-line-strong bg-transparent font-mono text-[10.5px] font-normal text-muted-foreground",
};

const WARN_CHIP =
  "rounded-full border-resin/40 bg-resin/8 font-mono text-[10px] font-normal text-resin";

const STATE_LABELS: Record<string, string> = {
  stopped: "已停止",
  idle: "空闲",
  active: "活跃",
  running: "运行中",
  draining: "排空中",
  disabled: "已停用",
};

function StateBadge({ thinker }: { thinker: ThinkerInfo }) {
  let label: string = STATE_LABELS[thinker.state] ?? thinker.state;
  if (thinker.state === "running" && thinker.pid != null)
    label = `运行中 (PID ${thinker.pid})`;
  return <Badge className={STATE_STYLES[thinker.state]}>{label}</Badge>;
}

/** Pull bundled thinker code into the identity (thinker-sync POST). */
function usePullMutation(identityId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (names: string[]) => pullThinkerSync(identityId, names),
    onSuccess: (result) => {
      const changed = result.results.filter((r) => r.action !== "unchanged");
      if (changed.length === 0) {
        toast.success("已是最新");
      } else {
        toast.success(
          changed.map((r) => `${r.action} ${r.name}`).join(", "),
          { description: "重启思考者以加载新代码。" }
        );
      }
      queryClient.invalidateQueries({ queryKey: ["thinker-sync", identityId] });
      queryClient.invalidateQueries({ queryKey: ["thinkers", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
}

function VersionCell({
  identityId,
  sync,
}: {
  identityId: string;
  sync: ThinkerSyncEntry | undefined;
}) {
  const controlsEnabled = useControlsEnabled();
  const pull = usePullMutation(identityId);
  if (!sync) return <TableCell />;
  return (
    <TableCell>
      <div className="flex items-center gap-1.5">
        {sync.bundled_version && (
          <span className="font-mono text-[10px] text-muted-foreground">
            {sync.bundled_version}
          </span>
        )}
        {sync.status === "outdated" && (
          <>
            <Badge
              className={WARN_CHIP}
              title={`与内置副本不一致的文件：${sync.changed_files.join(", ")}`}
            >
              有更新
            </Badge>
            {controlsEnabled && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-1.5 text-[11px]"
                disabled={pull.isPending}
                title={`拉取内置 ${sync.name}（${sync.changed_files.join(", ")}）；订阅关系与停用标记会保留`}
                onClick={() => pull.mutate([sync.name])}
              >
                <DownloadCloud className="size-3" />
                拉取
              </Button>
            )}
          </>
        )}
        {sync.status === "local_only" && (
          <Badge variant="outline" className="text-[10px]" title="无内置对应物——拉取操作不会触及它">
            仅本地
          </Badge>
        )}
      </div>
    </TableCell>
  );
}

function ThinkerRow({
  identityId,
  thinker,
  dispatcherRunning,
  sync,
}: {
  identityId: string;
  thinker: ThinkerInfo;
  dispatcherRunning: boolean;
  sync: ThinkerSyncEntry | undefined;
}) {
  const controlsEnabled = useControlsEnabled();
  const mutation = useThinkerMutation(identityId);
  const queryClient = useQueryClient();
  const toggleMutation = useMutation({
    mutationFn: (enabled: boolean) =>
      setThinkerEnabled(identityId, thinker.name, enabled),
    onSuccess: (result) => {
      if (result.disabled) {
        toast.success(`已停用 ${result.name}——全部启动会跳过它`);
      } else if (result.needs_restart) {
        toast.success(`已启用 ${result.name}`, {
          description:
            "运行中的调度器还看不到它的订阅——需要停止再启动思考者才能生效。",
        });
      } else {
        toast.success(`已启用 ${result.name}`);
      }
      queryClient.invalidateQueries({ queryKey: ["thinkers", identityId] });
      queryClient.invalidateQueries({ queryKey: ["identities"] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const disabled = thinker.state === "disabled";
  const stopped = thinker.state === "stopped";
  return (
    <TableRow className={disabled ? "opacity-60" : undefined}>
      <TableCell className="font-mono font-medium">{thinker.name}</TableCell>
      <TableCell>
        <StateBadge thinker={thinker} />
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap gap-1">
          {thinker.types.map((type) => (
            <Badge key={type} variant="outline" className="text-[10px]">
              {type}
            </Badge>
          ))}
        </div>
      </TableCell>
      <TableCell className="font-mono text-[11px] text-muted-foreground">
        {thinker.log_bytes != null
          ? `${formatBytes(thinker.log_bytes)} · ${formatRelativeTime(thinker.log_mtime)}`
          : "—"}
      </TableCell>
      <VersionCell identityId={identityId} sync={sync} />
      <TableCell className="text-right">
        {controlsEnabled && (
          <div className="flex justify-end gap-1">
            {!disabled && (
              <>
                <StartStopButtons
                  identityId={identityId}
                  names={[thinker.name]}
                  running={!stopped && dispatcherRunning}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  title="手动触发这个思考者一次"
                  aria-label={`手动触发 ${thinker.name} 一次`}
                  disabled={mutation.isPending}
                  onClick={() =>
                    mutation.mutate({ action: "step", names: [thinker.name] })
                  }
                >
                  <Zap className="size-3" />
                  step
                </Button>
              </>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground"
              title={
                disabled
                  ? `启用 ${thinker.name}`
                  : `停用 ${thinker.name}——全部启动与调度器将跳过它`
              }
              aria-label={disabled ? `启用 ${thinker.name}` : `停用 ${thinker.name}`}
              disabled={toggleMutation.isPending}
              onClick={() => toggleMutation.mutate(disabled)}
            >
              {disabled ? "启用" : "停用"}
            </Button>
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}

function StatusPanel({ identityId }: { identityId: string }) {
  const controlsEnabled = useControlsEnabled();
  const pull = usePullMutation(identityId);
  const {
    data: status,
    isError: statusError,
    error: statusErrorObj,
    refetch: refetchStatus,
  } = useQuery({
    queryKey: ["thinkers", identityId],
    queryFn: () => fetchThinkers(identityId),
    refetchInterval: THINKERS_FEED_POLL_MS,
  });
  const { data: syncStatus } = useQuery({
    queryKey: ["thinker-sync", identityId],
    queryFn: () => fetchThinkerSync(identityId),
    staleTime: 30_000,
  });
  const syncByName = new Map(
    (syncStatus?.thinkers ?? []).map((entry) => [entry.name, entry])
  );
  const outdated = (syncStatus?.thinkers ?? []).filter(
    (entry) => entry.status === "outdated"
  );
  // _lib and bundled thinkers with no identity dir have no table row —
  // surface them in the footer strip below the table.
  const stripEntries = (syncStatus?.thinkers ?? []).filter(
    (entry) =>
      entry.name.startsWith("_") ||
      (entry.status === "not_installed" && !entry.name.startsWith("_"))
  );

  if (statusError) {
    return (
      <QueryErrorBanner
        error={statusErrorObj}
        onRetry={() => void refetchStatus()}
      />
    );
  }

  if (!status) {
    return (
      <div className="mb-6 space-y-3">
        <Skeleton className="h-12 w-full rounded-xl" />
        <Skeleton className="h-24 w-full rounded-xl" />
      </div>
    );
  }

  const dispatcherRunning = status.dispatcher.running;
  return (
    <div className="mb-6 space-y-3">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border bg-card px-4 py-3">
        <span className="text-sm font-medium">调度器</span>
        {dispatcherRunning ? (
          <Badge className={STATE_STYLES.active}>
            <span className="size-[5px] animate-pulse rounded-full bg-primary" />
            running (PID {status.dispatcher.pid})
          </Badge>
        ) : (
          <Badge className={STATE_STYLES.stopped}>
            <span className="size-[5px] rounded-full bg-faint" />
            已停止
          </Badge>
        )}
        <span className="font-mono text-xs text-muted-foreground">
          {status.active_thinkers}/{status.thinkers_total} 个思考者活跃
          {status.thinkers_disabled > 0 && ` · ${status.thinkers_disabled} 个已停用`}
        </span>
        <div className="ml-auto">
          <StartStopButtons
            identityId={identityId}
            names={[]}
            running={dispatcherRunning}
            startDisabled={dispatcherRunning}
            startDisabledReason="调度器已在运行——请先停止再启动，或逐个启动思考者"
          />
        </div>
      </div>
      {status.thinkers.length === 0 ? (
        <div className="py-4 text-center text-sm text-muted-foreground">
          此身份没有安装思考者。
        </div>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>思考者</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>订阅类型</TableHead>
                <TableHead>日志</TableHead>
                <TableHead>版本</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {status.thinkers.map((thinker) => (
                <ThinkerRow
                  key={thinker.name}
                  identityId={identityId}
                  thinker={thinker}
                  dispatcherRunning={dispatcherRunning}
                  sync={syncByName.get(thinker.name)}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {(stripEntries.length > 0 || outdated.length > 1) && (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border px-4 py-2 text-xs text-muted-foreground">
          {stripEntries.map((entry) => (
            <span key={entry.name} className="flex items-center gap-1.5">
              <span className="font-mono">{entry.name}</span>
              {entry.bundled_version && (
                <span className="font-mono text-[10px]">{entry.bundled_version}</span>
              )}
              {entry.status === "in_sync" && (
                <Badge variant="outline" className="text-[10px]">
                  已是最新
                </Badge>
              )}
              {entry.status === "outdated" && (
                <Badge className={WARN_CHIP}>有更新</Badge>
              )}
              {entry.status === "not_installed" && (
                <Badge variant="outline" className="text-[10px]">
                  未安装
                </Badge>
              )}
              {controlsEnabled &&
                (entry.status === "outdated" ||
                  entry.status === "not_installed") && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-1.5 text-[11px]"
                    disabled={pull.isPending}
                    onClick={() => pull.mutate([entry.name])}
                  >
                    <DownloadCloud className="size-3" />
                    {entry.status === "not_installed" ? "安装" : "拉取"}
                  </Button>
                )}
            </span>
          ))}
          {controlsEnabled && outdated.length > 1 && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto h-6 px-2 text-[11px]"
              disabled={pull.isPending}
              title={`拉取 ${outdated.map((entry) => entry.name).join(", ")}`}
              onClick={() => pull.mutate(outdated.map((entry) => entry.name))}
            >
              <DownloadCloud className="size-3" />
              全部拉取
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

function LogView({
  identityId,
  name,
  live,
}: {
  identityId: string;
  name: string;
  live: boolean;
}) {
  const [tailBytes, setTailBytes] = useState(65536);
  const { data: log } = useQuery({
    queryKey: ["log", identityId, name, tailBytes],
    queryFn: () => fetchLog(identityId, name, tailBytes),
    refetchInterval: pollWhileLive(live),
  });

  if (!log) {
    return (
      <div className="space-y-2 pt-1">
        <Skeleton className="h-3 w-40" />
        <Skeleton className="h-40 w-full rounded-xl" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-1.5 flex items-center gap-2 font-mono text-[11px] text-muted-foreground">
        <span>共 {formatBytes(log.total_bytes)}</span>
        {log.truncated && (
          <button
            type="button"
            onClick={() => setTailBytes((n) => n * 4)}
            className="hover:underline"
          >
            仅显示最后 {formatBytes(tailBytes)} — 加载更多
          </button>
        )}
      </div>
      <pre className="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words rounded-lg border bg-card p-3 font-mono text-[11px]">
        {log.content || "（空）"}
      </pre>
    </div>
  );
}

function DispatchView({
  identityId,
  live,
}: {
  identityId: string;
  live: boolean;
}) {
  const { data: events } = useQuery({
    queryKey: ["dispatch", identityId],
    queryFn: () => fetchDispatch(identityId),
    refetchInterval: pollWhileLive(live),
  });

  if (!events) {
    return (
      <div className="space-y-1.5 pt-1">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-4 w-full" />
        ))}
      </div>
    );
  }
  if (events.length === 0) {
    return (
      <div className="py-10 text-center text-sm text-muted-foreground">
        未发现 dispatcher.log（调度器事件流待落盘）。
      </div>
    );
  }

  return (
    <div className="max-h-[70vh] overflow-auto rounded-lg border bg-card">
      {events.map((event, idx) => (
        <div
          key={idx}
          className={cn(
            "flex items-center gap-2 border-b px-3 py-1 font-mono text-[11px] last:border-b-0",
            event.kind === "dispatch" && "bg-moss/[0.06]"
          )}
        >
          {event.kind === "step" && (
            <>
              <span className="text-muted-foreground">步骤</span>
              <Badge variant="outline" className="text-[10px]">
                {event.type}
              </Badge>
              {event.source && (
                <span className="text-muted-foreground">来自 {event.source}</span>
              )}
            </>
          )}
          {event.kind === "dispatch" && (
            <>
              <span className="font-medium text-moss">
                dispatch → {event.thinker}
              </span>
              {event.active != null && (
                <span className="text-muted-foreground">active={event.active}</span>
              )}
            </>
          )}
          {event.kind === "other" && (
            <span className="text-muted-foreground">{event.raw}</span>
          )}
        </div>
      ))}
    </div>
  );
}

export default function ThinkersPage() {
  const { identityId = "" } = useParams();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_ACTIVE_POLL_MS,
  });
  const live = status?.live ?? false;

  const {
    data: logs,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["logs", identityId],
    queryFn: () => fetchLogs(identityId),
    refetchInterval: pollWhileLive(live),
  });

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <div className="mb-6 space-y-3">
          <Skeleton className="h-12 w-full rounded-xl" />
          <Skeleton className="h-24 w-full rounded-xl" />
        </div>
        <Skeleton className="h-8 w-64" />
      </div>
    );
  }

  const logNames = (logs ?? [])
    .map((l) => l.name)
    .filter((name) => name !== "dispatcher.log");

  return (
    <div className="mx-auto w-full max-w-7xl">
      <StatusPanel identityId={identityId} />
      {!logs || logs.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>暂无思考者日志</EmptyTitle>
            <EmptyDescription>
              此身份还没有 run/*.log 日志文件。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Tabs defaultValue="dispatch">
          <TabsList>
            <TabsTrigger value="dispatch" className="font-mono text-xs">
              dispatch
            </TabsTrigger>
            {logNames.map((name) => (
              <TabsTrigger key={name} value={name} className="font-mono text-xs">
                {name.replace(/\.log$/, "")}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value="dispatch">
            <DispatchView identityId={identityId} live={live} />
          </TabsContent>
          {logNames.map((name) => (
            <TabsContent key={name} value={name}>
              <LogView identityId={identityId} name={name} live={live} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  );
}
