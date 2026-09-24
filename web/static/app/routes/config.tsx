import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Info, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { ModelConfigSection } from "~/components/model-config";
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

export function meta() {
  return [{ title: "Headlong · config" }];
}

function useEnvMutations(identityId: string) {
  const queryClient = useQueryClient();
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["env", identityId] });
  const save = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      putEnvVar(identityId, key, value),
    onSuccess: (entry) => {
      toast.success(`Saved ${entry.key}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const remove = useMutation({
    mutationFn: (key: string) => deleteEnvVar(identityId, key),
    onSuccess: (result) => {
      toast.success(`Removed ${result.key}`);
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
      {entry.value || <span className="text-muted-foreground">(empty)</span>}
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
                entry.secret ? "enter new value (replaces current)" : entry.value
              }
              className="h-8 flex-1 font-mono text-xs"
            />
            <Button type="submit" size="sm" disabled={save.isPending}>
              Save
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setEditing(false)}
            >
              Cancel
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
              title={`Edit ${entry.key}`}
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
              title={`Remove ${entry.key}`}
              disabled={remove.isPending}
              onClick={() => {
                if (window.confirm(`Remove ${entry.key} from this identity's .env?`))
                  remove.mutate(entry.key);
              }}
            >
              <Trash2 className="size-3" />
            </Button>
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
        placeholder="VARIABLE_NAME"
        pattern="[A-Za-z_][A-Za-z0-9_]*"
        title="letters, digits, underscores"
        className="h-8 w-56 font-mono text-xs"
      />
      <Input
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="value"
        className="h-8 flex-1 font-mono text-xs"
      />
      <Button type="submit" size="sm" disabled={save.isPending || !key.trim()}>
        <Plus className="size-3" />
        Add
      </Button>
    </form>
  );
}

function formatAgo(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

function formatWhen(iso: string): string {
  const when = new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
  return `${when} (${formatAgo(iso)})`;
}

function formatBytes(n: number): string {
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
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
      q.state.data?.some((j) => j.status === "running") ? 1500 : false,
  });
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ["export-jobs", identityId] });
  const start = useMutation({
    mutationFn: () => startExportJob(identityId, { soulOnly, slim }),
    onSuccess: refresh,
    onError: (e: Error) => toast.error(`Export failed to start: ${e.message}`),
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
          export
        </h2>
        <span className="text-[11px] text-muted-foreground">
          Snapshot this identity as a portable .tgz — import it on another
          Headlong dash (or with `identity import`). Secrets (.env) and runtime
          state never leave the box.
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
              <LoadingDots text="Building" />
            ) : (
              <>
                <Download className="size-3" />
                Build export
              </>
            )}
          </Button>
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Checkbox
              checked={slim}
              disabled={running}
              onCheckedChange={(checked) => setSlim(checked === true)}
            />
            slim
            <Tooltip>
              <TooltipTrigger asChild>
                <Info className="size-3 shrink-0 cursor-help" />
              </TooltipTrigger>
              <TooltipContent className="max-w-sm text-xs">
                <p className="mb-1">
                  <b>Slim</b> (on): every step is kept, but the two fields that
                  repeat in each step — the rendered prompt context and the
                  shellm launch command line — are cut to a short head plus
                  &ldquo;…[truncated N chars]&rdquo;. API keys are replaced with
                  [REDACTED:…]. Thoughts, messages, reasoning and shell output
                  travel whole, as do memories, blobs and the workdir. Roughly a
                  tenth of the fat size (Audel: 1 GB of trajectories, 92 MB
                  archive) and still imports.
                </p>
                <p>
                  <b>Fat</b> (off): a byte-for-byte copy of the trajectories,
                  including any keys that leaked into them. Use it for a real
                  backup or to replay exact prompts; roughly a third of the raw
                  size once gzipped.
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
            soul only — skip trajectories (memories, thinkers, and skills; the
            import starts a fresh mind log)
          </label>
        </div>
        {running && (
          <span className="text-xs text-muted-foreground">
            Building on the server. Big mind logs take a minute or two; the
            build keeps going if you leave this page, and the file will be
            listed here when you come back.
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
                    `building… ${Math.round(job.seconds)}s`}
                  {job.status === "done" &&
                    `${formatWhen(job.started_at)}, built in ${Math.round(job.seconds)}s`}
                  {job.status === "failed" &&
                    `failed: ${job.error ?? "unknown error"}`}
                </span>
                {job.status !== "running" && (
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    title="Delete this export from the server"
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
    refetchInterval: 5000,
  });

  const { data: env, isLoading } = useQuery({
    queryKey: ["env", identityId],
    queryFn: () => fetchIdentityEnv(identityId),
  });

  if (isLoading || !env) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl px-4">
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
            identity .env
          </h2>
          <span className="text-[11px] text-muted-foreground">{env.note}</span>
        </div>
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-64">Variable</TableHead>
                <TableHead>Value</TableHead>
                <TableHead className="w-24 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {env.env.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className="py-6 text-center text-sm text-muted-foreground"
                  >
                    No identity-specific variables yet.
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
            inherited from serve root .env
          </h2>
          <span className="text-[11px] text-muted-foreground">
            Applies to every identity; add a variable above to override it here.
          </span>
        </div>
        <div className="rounded-lg border">
          <Table>
            <TableBody>
              {env.inherited.length === 0 && (
                <TableRow>
                  <TableCell className="py-6 text-center text-sm text-muted-foreground">
                    No .env at the serve root.
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
                        overridden
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
