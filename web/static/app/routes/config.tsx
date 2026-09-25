import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Info, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { ModelConfigSection } from "~/components/model-config";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import {
  deleteEnvVar,
  deleteExportJob,
  exportJobDownloadUrl,
  fetchExportJobs,
  fetchIdentityEnv,
  fetchIdentityStatus,
  putEnvVar,
  startExportJob,
} from "~/lib/api";
import type { EnvEntry } from "~/lib/types";
import { formatBytes, formatRelativeTime } from "~/lib/format";
import {
  JOB_PROGRESS_POLL_MS,
  STATUS_BACKGROUND_POLL_MS,
} from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 配置" }];
}

function useEnvMutations(identityId: string) {
  const queryClient = useQueryClient();
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["env", identityId] });
  const save = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      putEnvVar(identityId, key, value),
    onSuccess: (entry) => {
      toast.success(`已保存 ${entry.key}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const remove = useMutation({
    mutationFn: (key: string) => deleteEnvVar(identityId, key),
    onSuccess: (result) => {
      toast.success(`已移除 ${result.key}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });
  return { save, remove };
}

function ValueDisplay({ entry }: { entry: EnvEntry }) {
  return (
    <span className="inline-flex items-center gap-1.5 font-mono text-xs">
      {entry.secret && (
        <KeyRound className="size-3 shrink-0 text-muted-foreground" />
      )}
      {entry.value || <span className="text-muted-foreground">（空）</span>}
    </span>
  );
}

function EnvRow({
  identityId,
  entry,
}: {
  identityId: string;
  entry: EnvEntry;
}) {
  const controlsEnabled = useControlsEnabled();
  const { save, remove } = useEnvMutations(identityId);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [confirmRemove, setConfirmRemove] = useState(false);

  return (
    <TableRow>
      <TableCell className="font-mono text-xs font-medium">{entry.key}</TableCell>
      <TableCell>
        {editing ? (
          <form
            className="flex items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(
                { key: entry.key, value: draft },
                { onSuccess: () => setEditing(false) }
              );
            }}
          >
            <Input
              autoFocus
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              placeholder={
                entry.secret ? "输入新值（将替换当前值）" : entry.value
              }
              className="h-8 flex-1 font-mono text-xs"
            />
            <Button type="submit" size="sm" disabled={save.isPending}>
              保存
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setEditing(false)}
            >
              取消
            </Button>
          </form>
        ) : (
          <ValueDisplay entry={entry} />
        )}
      </TableCell>
      <TableCell className="text-right">
        {controlsEnabled && !editing && (
          <div className="flex justify-end gap-1">
            <Button
              variant="ghost"
              size="icon-sm"
              title={`编辑 ${entry.key}`}
              aria-label={`编辑 ${entry.key}`}
              onClick={() => {
                setDraft(entry.secret ? "" : entry.value);
                setEditing(true);
              }}
            >
              <Pencil className="size-3" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              title={`移除 ${entry.key}`}
              aria-label={`移除 ${entry.key}`}
              disabled={remove.isPending}
              onClick={() => setConfirmRemove(true)}
            >
              <Trash2 className="size-3" />
            </Button>
            <ConfirmDialog
              open={confirmRemove}
              onOpenChange={setConfirmRemove}
              tone="danger"
              title={`移除 ${entry.key}？`}
              description="将从该身份的 .env 中删除此变量。"
              confirmText="移除"
              onConfirm={() => remove.mutate(entry.key)}
            />
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}

function AddVarForm({
  identityId,
  prefillKey,
  onDone,
}: {
  identityId: string;
  prefillKey: string;
  onDone: () => void;
}) {
  const { save } = useEnvMutations(identityId);
  const [key, setKey] = useState(prefillKey);
  const [value, setValue] = useState("");

  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (!key.trim()) return;
        save.mutate(
          { key: key.trim(), value },
          {
            onSuccess: () => {
              setKey("");
              setValue("");
              onDone();
            },
          }
        );
      }}
    >
      <Input
        value={key}
        onChange={(event) => setKey(event.target.value)}
        placeholder="变量名"
        pattern="[A-Za-z_][A-Za-z0-9_]*"
        title="仅字母、数字、下划线"
        className="h-8 w-56 font-mono text-xs"
      />
      <Input
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="值"
        className="h-8 flex-1 font-mono text-xs"
      />
      <Button type="submit" size="sm" disabled={save.isPending || !key.trim()}>
        <Plus className="size-3" />
        添加
      </Button>
    </form>
  );
}

function formatWhen(iso: string): string {
  const when = new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
  return `${when} (${formatRelativeTime(iso)})`;
}

function ExportSection({ identityId }: { identityId: string }) {
  const queryClient = useQueryClient();
  const [soulOnly, setSoulOnly] = useState(false);
  const [slim, setSlim] = useState(true);

  // Archives are built in the background on the server, which keeps the last
  // few per identity. Polling the list (rather than one job id held in page
  // state) means navigating away and back, or a second person opening the
  // page, sees the same builds and downloads. A synchronous download of a
  // big mind log sat silent for a minute and then died at Cloudflare's 100s
  // limit, which looked like nothing at all.
  const jobs = useQuery({
    queryKey: ["export-jobs", identityId],
    queryFn: () => fetchExportJobs(identityId),
    refetchInterval: (q) =>
      q.state.data?.some((j) => j.status === "running")
        ? JOB_PROGRESS_POLL_MS
        : false,
  });
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ["export-jobs", identityId] });
  const start = useMutation({
    mutationFn: () => startExportJob(identityId, { soulOnly, slim }),
    onSuccess: refresh,
    onError: (e: Error) => toast.error(`导出启动失败：${e.message}`),
  });
  const remove = useMutation({
    mutationFn: (jobId: string) => deleteExportJob(jobId),
    onSuccess: refresh,
    onError: (e: Error) => toast.error(`Could not delete export: ${e.message}`),
  });

  const running =
    start.isPending ||
    (jobs.data?.some((j) => j.status === "running") ?? false);
  return (
    <section className="mt-8">
      <div className="mb-2 flex items-baseline gap-3">
        <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
          导出
        </h2>
        <span className="text-[11px] text-muted-foreground">
          把该身份打包为可携带的 .tgz——可在另一台仪表盘导入（或用
          identity import）。密钥（.env）与运行时状态永远不会离开本机。
        </span>
      </div>
      <div className="flex flex-col gap-3 rounded-lg border p-3">
        <div className="flex flex-wrap items-center gap-4">
          <Button
            variant="outline"
            size="sm"
            disabled={running}
            onClick={() => start.mutate()}
          >
            {running ? (
              <LoadingDots text="构建中" />
            ) : (
              <>
                <Download className="size-3" />
                构建导出
              </>
            )}
          </Button>
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Checkbox
              checked={slim}
              disabled={running}
              onCheckedChange={(checked) => setSlim(checked === true)}
            />
            精简
            <Tooltip>
              <TooltipTrigger asChild>
                <Info className="size-3 shrink-0 cursor-help" />
              </TooltipTrigger>
              <TooltipContent className="max-w-sm text-xs">
                <p className="mb-1">
                  <b>精简</b>（开）：排除 runs/ 工作现场，保留全部轨迹、
                  人格与记忆——.env 密钥与 run/ 控制面永远不进归档。
                  体积更小，适合日常备份与迁移，导入后可继续运行。
                </p>
                <p>
                  <b>完整</b>（关）：身份目录的完整复制（同样排除 .env
                  与 run/），用于逐字节备份或精确重放。
                </p>
              </TooltipContent>
            </Tooltip>
          </label>
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Checkbox
              checked={soulOnly}
              disabled={running}
              onCheckedChange={(checked) => setSoulOnly(checked === true)}
            />
            仅灵魂——跳过轨迹（只带人格与记忆；导入后从全新的日志开始）
          </label>
        </div>
        {running && (
          <span className="text-xs text-muted-foreground">
            服务器正在构建。大日志需要一两分钟；离开本页构建仍会继续，
            回来后文件会列在这里。
          </span>
        )}
        {jobs.data && jobs.data.length > 0 && (
          <ul className="flex flex-col gap-1 text-xs">
            {jobs.data.map((job) => (
              <li
                key={job.job_id}
                className="flex flex-wrap items-center gap-3"
              >
                {job.status === "done" ? (
                  <Button size="sm" variant="secondary" asChild>
                    <a
                      href={exportJobDownloadUrl(job)}
                      download={job.filename ?? undefined}
                    >
                      <Download className="size-3" />
                      {job.filename}
                      {job.size !== null && ` (${formatBytes(job.size)})`}
                    </a>
                  </Button>
                ) : (
                  <span
                    className={
                      job.status === "failed" ? "text-destructive" : "font-mono"
                    }
                  >
                    {job.filename}
                  </span>
                )}
                <span className="text-muted-foreground">
                  {job.status === "running" &&
                    `构建中… ${Math.round(job.seconds)}s`}
                  {job.status === "done" &&
                    `${formatWhen(job.started_at)} 构建，耗时 ${Math.round(job.seconds)}s`}
                  {job.status === "failed" &&
                    `失败：${job.error ?? "未知错误"}`}
                </span>
                {job.status !== "running" && (
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    title="从服务器删除此条目"
                    onClick={() => remove.mutate(job.job_id)}
                  >
                    <Trash2 className="size-3" />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

export default function ConfigPage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();
  const [prefillKey, setPrefillKey] = useState("");

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const {
    data: env,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["env", identityId],
    queryFn: () => fetchIdentityEnv(identityId),
  });

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <IdentityTabs identityId={identityId} live={false} active="config" />
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading || !env) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={status?.live ?? false}
        active="config"
      />
      <div className="mx-auto w-full max-w-4xl">

      <ModelConfigSection identityId={identityId} env={env} />

      <section className="mb-8">
        <div className="mb-2 flex items-baseline gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            身份 .env
          </h2>
          <span className="text-[11px] text-muted-foreground">{env.note}</span>
        </div>
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-64">变量</TableHead>
                <TableHead>值</TableHead>
                <TableHead className="w-24 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {env.env.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className="py-6 text-center text-sm text-muted-foreground"
                  >
                    还没有身份级变量。
                  </TableCell>
                </TableRow>
              )}
              {env.env.map((entry) => (
                <EnvRow key={entry.key} identityId={identityId} entry={entry} />
              ))}
            </TableBody>
          </Table>
        </div>
        {controlsEnabled && (
          <div className="mt-3">
            <AddVarForm
              key={prefillKey}
              identityId={identityId}
              prefillKey={prefillKey}
              onDone={() => setPrefillKey("")}
            />
          </div>
        )}
      </section>

      <section>
        <div className="mb-2 flex items-baseline gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            继承自服务根 .env
          </h2>
          <span className="text-[11px] text-muted-foreground">
            对所有身份生效；在上方添加同名变量即可在此身份覆盖它。
          </span>
        </div>
        <div className="rounded-lg border">
          <Table>
            <TableBody>
              {env.inherited.length === 0 && (
                <TableRow>
                  <TableCell className="py-6 text-center text-sm text-muted-foreground">
                    服务根目录没有 .env。
                  </TableCell>
                </TableRow>
              )}
              {env.inherited.map((entry) => (
                <TableRow key={entry.key}>
                  <TableCell className="w-64 font-mono text-xs">
                    {entry.key}
                  </TableCell>
                  <TableCell>
                    <ValueDisplay entry={entry} />
                    {entry.overridden && (
                      <Badge variant="outline" className="ml-2 text-[10px]">
                        已覆盖
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="w-24 text-right">
                    {controlsEnabled && !entry.overridden && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setPrefillKey(entry.key)}
                      >
                        Override
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </section>

      <ExportSection identityId={identityId} />
      </div>
    </div>
  );
}
