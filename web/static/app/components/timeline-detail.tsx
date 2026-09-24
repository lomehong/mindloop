// Detail modal for the Timeline tab: a clicked step square shows its full
// step card; a clicked run block shows the run summary plus its machinery
// steps. Both are enriched with causal context — what triggered this, what
// run it belongs to, what the run produced — and those rows navigate to the
// related item's modal. A modal (not inline expansion) so the live timeline
// never reflows.

import { useQuery } from "@tanstack/react-query";
import {
  Check,
  ChevronDown,
  ChevronRight,
  CornerDownRight,
  Link,
  Play,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";

import { ExpandableText } from "~/components/expandable-text";
import { StepCard } from "~/components/step-card";
import { Badge } from "~/components/ui/badge";
import { fetchRunCommand } from "~/lib/api";
import { stepColor } from "~/lib/step-colors";
import type { TimelineBlock } from "~/lib/timeline-model";
import { useTrajContext } from "~/lib/traj-context";
import type { NormalizedStep, RunGroup } from "~/lib/types";
import { cn } from "~/lib/utils";

/** The mindlog payload truncates huge run commands; fetch the full text
 * when the detail opens. Falls back to the truncated copy (e.g. runs in
 * sub-trajectories, which the command endpoint doesn't cover). */
function RunCommand({ run }: { run: RunGroup }) {
  const traj = useTrajContext();
  const { data } = useQuery({
    queryKey: ["run-command", traj?.identityId, run.run_id],
    queryFn: () => fetchRunCommand(traj?.identityId ?? "", run.run_id),
    enabled: Boolean(run.command_truncated && traj?.identityId),
    staleTime: Infinity,
    retry: false,
  });
  return (
    <ExpandableText text={data?.command ?? run.command} expandAll={false} mono />
  );
}

export type TimelineSelection =
  | { kind: "step"; step: NormalizedStep }
  | { kind: "run"; block: TimelineBlock };

/** Copy `getUrl()` to the clipboard and flash `copied` for 1.5s; a denied
 * clipboard (permissions, non-secure context) is silently ignored. */
function useCopyLink(getUrl: () => string) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard
      ?.writeText(getUrl())
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {});
  };
  return { copied, copy };
}

/** While the modal is open the URL carries ?step=/?run= for the selected
 * item, so the copy just lifts the address bar. */
function CopyLinkButton() {
  const { copied, copy } = useCopyLink(() => window.location.href);
  return (
    <button
      type="button"
      aria-label="Copy link"
      title="Copy link to this item"
      className="absolute right-10 top-3 rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      onClick={copy}
    >
      {copied ? (
        <Check className="h-4 w-4 text-green-500" />
      ) : (
        <Link className="h-4 w-4" />
      )}
    </button>
  );
}

/** Copy a deeplink to one run substep. While the run modal is open the
 * URL carries ?run=, not the substep, so the link is built by hand. */
function SubstepLink({ stepId }: { stepId: string }) {
  const { copied, copy } = useCopyLink(() => {
    const url = new URL(window.location.href);
    url.searchParams.set("step", stepId);
    url.searchParams.delete("run");
    return url.toString();
  });
  return (
    <button
      type="button"
      aria-label="Copy link to this step"
      title="Copy link to this step"
      className="absolute right-1 top-1.5 z-10 rounded-md p-1 text-muted-foreground opacity-0 hover:bg-accent hover:text-foreground focus-visible:opacity-100 group-hover/substep:opacity-100"
      onClick={copy}
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-green-500" />
      ) : (
        <Link className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

export function Modal({
  onClose,
  children,
}: {
  onClose: () => void;
  children: React.ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
    >
      <div
        className="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-lg border bg-background p-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <CopyLinkButton />
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="absolute right-3 top-3 rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
        {children}
      </div>
    </div>
  );
}

function durationOf(started: string, ended: string | null): string | null {
  if (!started || !ended) return null;
  const ms = new Date(ended).getTime() - new Date(started).getTime();
  if (Number.isNaN(ms) || ms < 0) return null;
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

function runTitle(block: TimelineBlock): string {
  if (block.run.tldr) return block.run.tldr;
  const cmd = block.run.command;
  const idx = cmd.lastIndexOf("ACTION:");
  if (idx >= 0) return cmd.slice(idx + 7).replace(/\s+/g, " ").trim();
  const prompt = block.members.find((m) => m.type === "prompt");
  return prompt?.preview ?? "shellm run";
}

/** Clickable "related item" row: triggered-by, belongs-to-run, etc. */
function RelatedStepRow({
  label,
  step,
  onClick,
}: {
  label: string;
  step: NormalizedStep;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full min-w-0 items-center gap-2 rounded border border-transparent px-1.5 py-1 text-left hover:border-border hover:bg-accent/50"
    >
      <CornerDownRight className="h-3 w-3 shrink-0 text-muted-foreground" />
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{label}</span>
      <span
        className={cn(
          "shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px]",
          stepColor(step.type).chip
        )}
      >
        {step.type}
      </span>
      {step.source && (
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {step.source}
        </span>
      )}
      <span className="min-w-0 flex-1 truncate text-xs">{step.preview}</span>
    </button>
  );
}

function RelatedRunRow({
  label,
  block,
  onClick,
}: {
  label: string;
  block: TimelineBlock;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full min-w-0 items-center gap-2 rounded border border-transparent px-1.5 py-1 text-left hover:border-border hover:bg-accent/50"
    >
      <CornerDownRight className="h-3 w-3 shrink-0 text-muted-foreground" />
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{label}</span>
      <Play className="h-3 w-3 shrink-0 text-blue-500" />
      {block.run.launched_by && (
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {block.run.launched_by}
        </span>
      )}
      <span className="min-w-0 flex-1 truncate text-xs italic">{runTitle(block)}</span>
    </button>
  );
}

export function TimelineDetailModal({
  selected,
  onClose,
  onSelect,
  stepById,
  blockByRun,
}: {
  selected: TimelineSelection;
  onClose: () => void;
  onSelect: (next: TimelineSelection) => void;
  stepById: Map<string, NormalizedStep>;
  blockByRun: Map<string, TimelineBlock>;
}) {
  if (selected.kind === "step") {
    const { step } = selected;
    const triggerId =
      typeof step.raw.trigger_step === "string" ? step.raw.trigger_step : null;
    const trigger = triggerId ? stepById.get(triggerId) : undefined;
    const runId = typeof step.raw.run_id === "string" ? step.raw.run_id : null;
    const runBlock = runId ? blockByRun.get(runId) : undefined;
    // this step may itself have triggered runs
    const triggeredRuns = [...blockByRun.values()].filter(
      (b) => b.run.trigger_step_id === step.step_id
    );
    const hasContext = trigger || runBlock || triggeredRuns.length > 0;
    return (
      <Modal onClose={onClose}>
        <div className="pr-14">
          <StepCard step={step} expandAll />
          {hasContext && (
            <div className="mt-3 space-y-0.5 border-t pt-2">
              {trigger && (
                <RelatedStepRow
                  label="triggered by"
                  step={trigger}
                  onClick={() => onSelect({ kind: "step", step: trigger })}
                />
              )}
              {runBlock && (
                <RelatedRunRow
                  label="written inside"
                  block={runBlock}
                  onClick={() => onSelect({ kind: "run", block: runBlock })}
                />
              )}
              {triggeredRuns.map((b) => (
                <RelatedRunRow
                  key={b.run.run_id}
                  label="triggered run"
                  block={b}
                  onClick={() => onSelect({ kind: "run", block: b })}
                />
              ))}
            </div>
          )}
        </div>
      </Modal>
    );
  }

  return <RunModal selected={selected} onClose={onClose} onSelect={onSelect} stepById={stepById} />;
}

function RunModal({
  selected,
  onClose,
  onSelect,
  stepById,
}: {
  selected: Extract<TimelineSelection, { kind: "run" }>;
  onClose: () => void;
  onSelect: (next: TimelineSelection) => void;
  stepById: Map<string, NormalizedStep>;
}) {
  // High level (tldr / trigger / result) up top; machinery on demand.
  const [showSteps, setShowSteps] = useState(false);
  const { block } = selected;
  const { run } = block;
  const duration = durationOf(run.started_ts, run.ended_ts);
  const trigger = run.trigger_step_id ? stepById.get(run.trigger_step_id) : undefined;
  const final = block.members.find((m) => m.type === "final");
  const result = typeof final?.raw.content === "string" ? final.raw.content : null;
  return (
    <Modal onClose={onClose}>
      <div className="pr-14">
        <div className="mb-1 flex flex-wrap items-center gap-2">
          <Badge variant="outline" className="text-[10px]">
            run
          </Badge>
          {run.launched_by && (
            <span className="font-mono text-xs text-muted-foreground">
              launched by <span className="text-foreground">{run.launched_by}</span>
            </span>
          )}
          <span className="ml-auto flex items-center gap-2 font-mono text-[10px] text-muted-foreground">
            <span>{run.status}</span>
            {duration && <span>{duration}</span>}
            {run.model && <span>{run.model}</span>}
          </span>
        </div>
        {run.tldr && <p className="mb-1 text-sm italic">{run.tldr}</p>}
        <div className="mb-2 text-muted-foreground">
          <RunCommand run={run} />
        </div>
        {(trigger || result) && (
          <div className="mb-2 space-y-1 border-t pt-2">
            {trigger && (
              <RelatedStepRow
                label="triggered by"
                step={trigger}
                onClick={() => onSelect({ kind: "step", step: trigger })}
              />
            )}
            {result && (
              <div className="rounded border bg-muted/30 px-2 py-1.5">
                <div className="mb-0.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                  result
                </div>
                <ExpandableText text={result} expandAll={false} />
              </div>
            )}
          </div>
        )}
        <div className="border-t pt-1.5">
          <button
            type="button"
            onClick={() => setShowSteps((v) => !v)}
            aria-expanded={showSteps}
            className="flex w-full items-center gap-1.5 rounded px-1 py-1 text-left font-mono text-[11px] text-muted-foreground hover:bg-accent/50 hover:text-foreground"
          >
            {showSteps ? (
              <ChevronDown className="h-3.5 w-3.5" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5" />
            )}
            steps ({block.members.length})
          </button>
          {showSteps && (
            <div className="mt-1 space-y-0.5">
              {block.members.map((step) => (
                <div key={step.step_id} className="group/substep relative">
                  <SubstepLink stepId={step.step_id} />
                  <StepCard step={step} expandAll={false} />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}
