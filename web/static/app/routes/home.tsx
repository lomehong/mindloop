import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Plus, Skull, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { StartStopButtons, useControlsEnabled } from "~/components/thinker-controls";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { AuthenticatedDownload } from "~/components/authenticated-download";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import {
  createIdentity,
  exportAllUrl,
  fetchIdentities,
  importIdentities,
  killAll,
} from "~/lib/api";
import type { Identity } from "~/lib/types";
import { formatRelativeTime } from "~/lib/format";
import { STATUS_BACKGROUND_POLL_MS } from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 身份" }];
}

function LiveBadge({ live }: { live: boolean }) {
  if (!live) return null;
  return (
    <Badge className="gap-1.5 bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300">
      <span className="relative flex h-2 w-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-green-500" />
      </span>
      运行中
    </Badge>
  );
}

function DispatcherCell({ identity }: { identity: Identity }) {
  if (identity.dispatcher?.running) {
    return (
      <span className="font-mono text-xs text-green-700 dark:text-green-400">
        ● PID {identity.dispatcher.pid}
      </span>
    );
  }
  return <span className="text-xs text-muted-foreground">已停止</span>;
}

function NewIdentityForm() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: createIdentity,
    onSuccess: (created) => {
      toast.success(`Created identity ${created.name}`);
      queryClient.invalidateQueries({ queryKey: ["identities"] });
      setOpen(false);
      setName("");
      navigate(`/i/${encodeURIComponent(created.id)}/thinkers`);
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (!open) {
    return (
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
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
          ? `Imported identity ${names[0]}`
          : `Imported ${names.length} identities: ${names.join(", ")}`
      );
      queryClient.invalidateQueries({ queryKey: ["identities"] });
      setOpen(false);
      setFile(null);
      setName("");
      if (result.imported.length === 1)
        navigate(`/i/${encodeURIComponent(result.imported[0].id)}/thinkers`);
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (!open) {
    return (
      <Button
        variant="outline"
        size="sm"
        title="从归档导入身份"
        onClick={() => setOpen(true)}
      >
        <Upload className="size-3" />
        Import
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
        variant="destructive"
        size="sm"
        disabled={mutation.isPending}
        title="停止本机全部 mindloop 进程（调度器、agent、思考者步骤）"
        onClick={() => mutation.mutate(true)}
      >
        <Skull className="size-3" />
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

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl space-y-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">身份</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            本机全部心智身份及其实时运行状态。
          </p>
        </div>
        <QueryErrorBanner
          error={error}
          onRetry={() => void refetch()}
        />
      </div>
    );
  }

  const groups = new Map<string, Identity[]>();
  for (const identity of identities ?? []) {
    const list = groups.get(identity.group) ?? [];
    list.push(identity);
    groups.set(identity.group, list);
  }

  return (
    <div className="mx-auto w-full max-w-7xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">身份</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            本机全部心智身份及其实时运行状态。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {controlsEnabled && <NewIdentityForm />}
          {controlsEnabled && <ImportIdentityForm />}
          {(identities?.length ?? 0) > 0 && (
            <AuthenticatedDownload
              variant="outline"
              size="sm"
              title="导出全部身份"
              url={exportAllUrl()}
              filename="mindloop-identities.tgz"
            >
              <Download className="size-3" />
              导出全部
            </AuthenticatedDownload>
          )}
          {controlsEnabled && <KillAllButton />}
        </div>
      </div>
      {!identities || identities.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>未发现身份</EmptyTitle>
            <EmptyDescription>
              在服务根目录下没有找到含 info.txt（root_trajectory=）
              的身份目录。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        [...groups.entries()].map(([group, members]) => (
          <section key={group}>
            <h2 className="mb-2 font-mono text-xs text-muted-foreground">
              {group}
            </h2>
            <div className="rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>身份</TableHead>
                    <TableHead>调度器</TableHead>
                    <TableHead>思考者</TableHead>
                    <TableHead>最近活动</TableHead>
                    <TableHead className="text-right">步骤</TableHead>
                    {controlsEnabled && (
                      <TableHead className="text-right">操作</TableHead>
                    )}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {members.map((identity) => (
                    <TableRow key={identity.id}>
                      <TableCell>
                        <Link
                          to={`/i/${encodeURIComponent(identity.id)}`}
                          className="flex items-center gap-2 font-mono font-medium hover:underline"
                        >
                          {identity.name}
                          <LiveBadge live={identity.live} />
                        </Link>
                      </TableCell>
                      <TableCell>
                        <DispatcherCell identity={identity} />
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {identity.thinkers_total > 0
                          ? `${identity.thinkers_active}/${identity.thinkers_total} active`
                          : "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {formatRelativeTime(identity.last_activity_ts)}
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums">
                        {identity.step_count}
                      </TableCell>
                      {controlsEnabled && (
                        <TableCell className="text-right">
                          {identity.thinkers_total > 0 && (
                            <div className="flex justify-end">
                              <StartStopButtons
                                identityId={identity.id}
                                names={[]}
                                running={identity.dispatcher?.running ?? false}
                              />
                            </div>
                          )}
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </section>
        ))
      )}
    </div>
  );
}
