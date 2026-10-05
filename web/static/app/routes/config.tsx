import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Info, KeyRound, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ModelConfigSection } from "~/components/model-config";
import { ProviderProfilesSection } from "~/components/provider-profiles";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { AuthenticatedDownload } from "~/components/authenticated-download";
import { ConfirmDialog } from "~/components/confirm-dialog";
import { useControlsEnabled } from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import {
  deleteEnvVar,
  deleteExportJob,
  exportJobDownloadUrl,
  fetchExportJobs,
  fetchIdentityEnv,
  putEnvVar,
  startExportJob,
} from "~/lib/api";
import type { EnvEntry } from "~/lib/types";
import { formatBytes, formatRelativeTime } from "~/lib/format";
import { JOB_PROGRESS_POLL_MS } from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 配置" }];
}

/* 行控件样式（mockup .mono-k / .lr / .pill）。 */
const MONO_K = "font-mono text-[11.5px] font-medium";
const PILL_ON =
  "inline-flex items-center gap-1.5 rounded-full border border-primary/35 bg-primary/10 px-2 py-px font-mono text-[10.5px] text-primary";

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
    <span
      className={
        "inline-flex min-w-0 items-center gap-1.5 font-mono text-[11.5px] " +
        (entry.secret ? "text-muted-foreground" : "")
      }
    >
      {entry.secret && (
        <KeyRound className="size-3 shrink-0 text-faint" />
      )}
      {entry.value || <span className="text-faint">（空）</span>}
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
    <div className="grid grid-cols-1 items-center gap-x-3 gap-y-1.5 border-b border-line px-4 py-2 last:border-b-0 md:grid-cols-[minmax(0,1fr)_200px_120px]">
      <span className={MONO_K}>{entry.key}</span>
      <div className="min-w-0">
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
              className="h-7 flex-1 font-mono text-xs"
            />
            <Button
              type="submit"
              size="sm"
              className="h-6 px-2 text-[11px]"
              disabled={save.isPending}
            >
              保存
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-[11px]"
              onClick={() => setEditing(false)}
            >
              取消
            </Button>
          </form>
        ) : (
          <ValueDisplay entry={entry} />
        )}
      </div>
      <div className="flex justify-end gap-1">
        {controlsEnabled && !editing && (
          <>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-[11px] text-muted-foreground"
              title={`编辑 ${entry.key}`}
              aria-label={`编辑 ${entry.key}`}
              onClick={() => {
                setDraft(entry.secret ? "" : entry.value);
                setEditing(true);
              }}
            >
              编辑
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-[11px] text-muted-foreground hover:text-clay"
              title={`移除 ${entry.key}`}
              aria-label={`移除 ${entry.key}`}
              disabled={remove.isPending}
              onClick={() => setConfirmRemove(true)}
            >
              移除
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
          </>
        )}
      </div>
    </div>
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
      className="flex flex-wrap items-center gap-2.5 px-0.5 pt-2 pb-1"
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
        className="h-8 min-w-40 flex-1 font-mono text-xs"
      />
      <Button
        type="submit"
        size="sm"
        className="h-7 px-2.5 text-[12px]"
        disabled={save.isPending || !key.trim()}
      >
        <Plus className="size-3" />
        添加
      </Button>
      <span className="text-[12px] text-faint">仅字母、数字、下划线</span>
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
    onError: (e: Error) => toast.error(`删除导出任务失败：${e.message}`),
  });

  const running =
    start.isPending ||
    (jobs.data?.some((j) => j.status === "running") ?? false);
  return (
    <section className="rounded-xl border border-line bg-card px-4 pt-3.5 pb-2.5">
      <h2 className="text-[14.5px] font-semibold">导出</h2>
      <p className="text-[12px] leading-[1.65] text-faint">
        把该身份打包为可携带的 .tgz——可在另一台仪表盘导入（或用
        identity import）。密钥（.env）与运行时状态永远不会离开本机。
      </p>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line px-0.5 pt-2.5 pb-1">
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-[12px]"
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
        <label className="flex items-center gap-2 text-[12px] text-muted-foreground">
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
        <label className="flex items-center gap-2 text-[12px] text-muted-foreground">
          <Checkbox
            checked={soulOnly}
            disabled={running}
            onCheckedChange={(checked) => setSoulOnly(checked === true)}
          />
          仅灵魂——跳过轨迹（只带人格与记忆；导入后从全新的日志开始）
        </label>
      </div>
      {running && (
        <p className="px-0.5 py-1 text-[12px] leading-relaxed text-muted-foreground">
          服务器正在构建。大日志需要一两分钟；离开本页构建仍会继续，
          回来后文件会列在这里。
        </p>
      )}
      {jobs.data && jobs.data.length > 0 && (
        <div className="mt-1.5 overflow-hidden rounded-xl border border-line">
          {jobs.data.map((job) => {
            const meta =
              job.status === "done"
                ? [
                    job.size !== null ? formatBytes(job.size) : null,
                    `${formatWhen(job.started_at)} 构建`,
                    `耗时 ${Math.round(job.seconds)}s`,
                  ]
                    .filter(Boolean)
                    .join(" · ")
                : null;
            return (
              <div
                key={job.job_id}
                className="grid grid-cols-1 items-center gap-x-3 gap-y-1 border-b border-line px-4 py-2 last:border-b-0 md:grid-cols-[minmax(0,1fr)_auto_auto]"
              >
                <span className="truncate font-mono text-[11.5px]">
                  {job.filename ?? job.job_id}
                </span>
                <span className="font-mono text-[10.5px] text-faint">
                  {job.status === "running" &&
                    `构建中… ${Math.round(job.seconds)}s`}
                  {job.status === "done" && meta}
                  {job.status === "failed" && (
                    <span className="text-clay">
                      失败：{job.error ?? "未知错误"}
                    </span>
                  )}
                </span>
                <span className="flex items-center justify-end gap-1">
                  {job.status === "done" && (
                    <AuthenticatedDownload
                      size="sm"
                      variant="outline"
                      className="h-6 px-2 text-[11px]"
                      url={exportJobDownloadUrl(job)}
                      filename={job.filename ?? `${job.job_id}.tgz`}
                    >
                      下载
                    </AuthenticatedDownload>
                  )}
                  {job.status !== "running" && (
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      className="text-muted-foreground hover:text-clay"
                      title="从服务器删除此条目"
                      aria-label={`删除导出 ${job.job_id}`}
                      onClick={() => remove.mutate(job.job_id)}
                    >
                      <Trash2 className="size-3" />
                    </Button>
                  )}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

export default function ConfigPage() {
  const { identityId = "" } = useParams();
  const controlsEnabled = useControlsEnabled();
  const [prefillKey, setPrefillKey] = useState("");

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
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading || !env) {
    return (
      <div className="mx-auto w-full max-w-7xl">
        <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
          <Skeleton className="h-7 w-40" />
          <Skeleton className="h-36 w-full rounded-xl" />
          <Skeleton className="h-36 w-full rounded-xl" />
          <Skeleton className="h-36 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            配置
          </h1>
          <span className="text-[13px] text-muted-foreground">
            档案 · 档位 · 密钥 · 导出
          </span>
        </div>

        <ProviderProfilesSection identityId={identityId} />

        <ModelConfigSection identityId={identityId} env={env} />

        <section className="rounded-xl border border-line bg-card px-4 pt-3.5 pb-2.5">
          <h2 className="text-[14.5px] font-semibold">身份 .env</h2>
          <p className="mb-2 text-[12px] leading-[1.65] text-faint">
            {env.note}
          </p>
          <div className="overflow-hidden rounded-xl border border-line">
            <div className="hidden grid-cols-[minmax(0,1fr)_200px_120px] gap-x-3 border-b border-line px-4 py-2 font-mono text-[10.5px] tracking-[0.1em] text-faint md:grid">
              <span>变量</span>
              <span>值</span>
              <span />
            </div>
            {env.env.length === 0 && (
              <div className="px-4 py-5 text-center text-[12.5px] text-muted-foreground">
                还没有身份级变量。
              </div>
            )}
            {env.env.map((entry) => (
              <EnvRow key={entry.key} identityId={identityId} entry={entry} />
            ))}
          </div>
          {controlsEnabled && (
            <div className="mt-1">
              <AddVarForm
                key={prefillKey}
                identityId={identityId}
                prefillKey={prefillKey}
                onDone={() => setPrefillKey("")}
              />
            </div>
          )}
        </section>

        <section className="rounded-xl border border-line bg-card px-4 pt-3.5 pb-2.5">
          <h2 className="text-[14.5px] font-semibold">继承自服务根 .env</h2>
          <p className="mb-2 text-[12px] leading-[1.65] text-faint">
            对所有身份生效；在上方添加同名变量即可在此身份覆盖它。
          </p>
          {env.inherited.length === 0 && (
            <p className="border-t border-line px-0.5 py-2.5 text-[12.5px] text-muted-foreground">
              服务根目录没有 .env。
            </p>
          )}
          {env.inherited.map((entry) => (
            <div
              key={entry.key}
              className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-line px-0.5 py-2.5"
            >
              <span className={MONO_K}>{entry.key}</span>
              <ValueDisplay entry={entry} />
              {entry.overridden && <span className={PILL_ON}>已覆盖</span>}
              <span className="ml-auto" />
              {controlsEnabled && !entry.overridden && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-6 px-2 text-[11px] text-muted-foreground"
                  onClick={() => setPrefillKey(entry.key)}
                >
                  Override
                </Button>
              )}
            </div>
          ))}
        </section>

        <ExportSection identityId={identityId} />
      </div>
    </div>
  );
}
