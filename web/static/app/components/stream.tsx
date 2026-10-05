import { Fragment, useState } from "react";

import { formatClock } from "~/lib/format";
import { RunGroupBlock } from "~/components/run-group";
import { StepCard } from "~/components/step-card";
import type { NormalizedStep, RunGroup } from "~/lib/types";

export type StreamItem =
  | { kind: "step"; step: NormalizedStep }
  | {
      kind: "run";
      run: RunGroup;
      steps: NormalizedStep[];
      actionStep: NormalizedStep | null;
    }
  | { kind: "idle"; steps: NormalizedStep[] };

/**
 * Turn the flat step list into renderable items: inline runs become
 * collapsible blocks anchored at their shellm-run step, consecutive idles
 * fold into strips, and actions joined to a run render as that run's header.
 */
export function assembleStream(
  steps: NormalizedStep[],
  runs: RunGroup[]
): StreamItem[] {
  const runsById = new Map(runs.map((run) => [run.run_id, run]));
  // Only action-type triggers become run headers (and are hidden from the
  // stream). Other trigger types (thought, message) are joined in the data
  // but keep rendering as their own stream steps.
  const actionsById = new Map<string, NormalizedStep>();
  for (const run of runs) {
    if (run.trigger_step_id) {
      const trigger = steps.find((s) => s.step_id === run.trigger_step_id);
      if (trigger?.type === "action") actionsById.set(run.trigger_step_id, trigger);
    }
  }

  const items: StreamItem[] = [];
  const runItems = new Map<string, Extract<StreamItem, { kind: "run" }>>();

  for (const step of steps) {
    if (step.run_id && runsById.has(step.run_id)) {
      let runItem = runItems.get(step.run_id);
      if (!runItem) {
        const run = runsById.get(step.run_id)!;
        runItem = {
          kind: "run",
          run,
          steps: [],
          actionStep: run.trigger_step_id
            ? actionsById.get(run.trigger_step_id) ?? null
            : null,
        };
        runItems.set(step.run_id, runItem);
        items.push(runItem);
      }
      runItem.steps.push(step);
      continue;
    }
    if (actionsById.has(step.step_id)) continue; // rendered as its run's header
    if (step.type === "idle") {
      const last = items[items.length - 1];
      if (last?.kind === "idle") {
        last.steps.push(step);
      } else {
        items.push({ kind: "idle", steps: [step] });
      }
      continue;
    }
    items.push({ kind: "step", step });
  }
  return items;
}

export function IdleStrip({
  steps,
  expandAll,
}: {
  steps: NormalizedStep[];
  expandAll: boolean;
}) {
  const [show, setShow] = useState(false);
  const first = formatClock(steps[0].ts);
  const last = formatClock(steps[steps.length - 1].ts);
  return (
    <div className="my-1">
      <button
        type="button"
        onClick={() => setShow((v) => !v)}
        className="flex w-full items-center gap-2 py-0.5 text-[11px] text-muted-foreground/70 hover:text-muted-foreground"
      >
        <span className="h-px flex-1 bg-border" />
        <span className="font-mono">
          空闲 ×{steps.length}（{first}–{last}）{show ? "收起" : "展开"}
        </span>
        <span className="h-px flex-1 bg-border" />
      </button>
      {show &&
        steps.map((step) => (
          <StepCard key={step.step_id} step={step} expandAll={expandAll} />
        ))}
    </div>
  );
}

/** 时间戳轨包装：日志流视图给每条目一行——左列 mono 时刻（发线分栏），
 * 右列渲染条目本体。条目自身的时间戳语义：step=其 ts、run=开始时刻、
 * idle=链首 ts。 */
function RailRow({
  ts,
  children,
}: {
  ts: string;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[52px_1fr] gap-2">
      <div className="pt-[13px] text-right font-mono text-[10px] leading-none tabular-nums text-faint">
        {formatClock(ts)}
      </div>
      <div className="min-w-0 border-l border-line pl-3">{children}</div>
    </div>
  );
}

export function StreamItems({
  items,
  expandAll,
  live = false,
  rail = false,
}: {
  items: StreamItem[];
  expandAll: boolean;
  live?: boolean;
  /** 日志流视图：每条目加时间戳轨（左列时刻 + 发线）。 */
  rail?: boolean;
}) {
  return (
    <>
      {items.map((item) => {
        let key: string;
        let node: React.ReactNode;
        let ts: string;
        if (item.kind === "run") {
          key = item.run.run_id;
          ts = item.run.started_ts;
          node = (
            <RunGroupBlock
              run={item.run}
              actionStep={item.actionStep}
              steps={item.steps}
              expandAll={expandAll}
              live={live}
            />
          );
        } else if (item.kind === "idle") {
          key = item.steps[0].step_id;
          ts = item.steps[0].ts;
          node = <IdleStrip steps={item.steps} expandAll={expandAll} />;
        } else {
          key = item.step.step_id;
          ts = item.step.ts;
          node = <StepCard step={item.step} expandAll={expandAll} />;
        }
        if (!rail) {
          return (
            <Fragment key={key}>{node}</Fragment>
          );
        }
        return (
          <RailRow key={key} ts={ts}>
            {node}
          </RailRow>
        );
      })}
    </>
  );
}
