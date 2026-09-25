// Memoized leaf renderers for the timeline canvas. Every prop is either a
// primitive, a module-constant palette, or an object with a stable identity
// (RunGroup / NormalizedStep keep identity across polls via use-mindlog's
// merge), so hover flips and poll ticks skip untouched cells entirely.

import {
  ArrowDownToLine,
  ChevronLeft,
  ChevronRight,
  ChevronsLeftRight,
  ChevronsRightLeft,
  EyeOff,
  Pause,
} from "lucide-react";
import { memo, useCallback, useState } from "react";

import { StepModal } from "~/components/mindlog-search";
import {
  Modal,
  TimelineDetailModal,
  type TimelineSelection,
} from "~/components/timeline-detail";
import { EDGE_STROKE, CELL, type CanvasPalette } from "~/components/timeline-palette";
import { edgeStyle } from "~/components/timeline-geometry";
import { timelineColor } from "~/lib/step-colors";
import type {
  TimelineBlock,
  TimelineEdge,
  TimelineLane,
} from "~/lib/timeline-model";
import type { NormalizedStep, RunGroup } from "~/lib/types";
import { cn } from "~/lib/utils";

export interface TimelineRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/** Hover state for the canvas: the id of the hovered cell/run (or null)
 * plus a stable setter for the memoized leaves. Pair with edgeStyle() from
 * timeline-geometry for the ghost-edge visuals. */
export function useHoverGhost() {
  const [hovered, setHovered] = useState<string | null>(null);
  const setHover = useCallback((id: string | null) => setHovered(id), []);
  return { hovered, setHover };
}

interface RunBlockButtonProps {
  run: RunGroup;
  title: string;
  rect: TimelineRect;
  running: boolean;
  hot: boolean;
  fresh: boolean;
  flash: boolean;
  palette: CanvasPalette;
  onOpen: (runId: string) => void;
  onHover: (id: string | null) => void;
}

/** The run's background block in its launcher's lane (summary chip paints
 * in a separate layer above the cells). */
export const RunBlockButton = memo(function RunBlockButton({
  run,
  title,
  rect,
  running,
  hot,
  fresh,
  flash,
  palette,
  onOpen,
  onHover,
}: RunBlockButtonProps) {
  return (
    <button
      type="button"
      onClick={() => onOpen(run.run_id)}
      onMouseEnter={() => onHover(run.run_id)}
      onMouseLeave={() => onHover(null)}
      className={cn(
        "absolute z-10 rounded-md border text-left",
        palette.block,
        running && "animate-pulse",
        hot && palette.blockRing,
        fresh && "tl-pop",
        flash && "tl-flash"
      )}
      style={{
        left: rect.left,
        width: rect.width,
        top: rect.top,
        height: rect.height,
        boxShadow: palette.blockShadow,
      }}
      title={`[run] ${title}`}
    />
  );
});

interface StepCellButtonProps {
  step: NormalizedStep;
  idleCount: number | undefined;
  idleSpan: string | undefined;
  rect: TimelineRect;
  lines: number;
  isCollapsed: boolean;
  hot: boolean;
  fresh: boolean;
  flash: boolean;
  palette: CanvasPalette;
  onOpen: (step: NormalizedStep) => void;
  onHover: (id: string | null) => void;
}

/** One step: colored square + clamped preview text. */
export const StepCellButton = memo(function StepCellButton({
  step,
  idleCount,
  idleSpan,
  rect,
  lines,
  isCollapsed,
  hot,
  fresh,
  flash,
  palette,
  onOpen,
  onHover,
}: StepCellButtonProps) {
  return (
    <button
      type="button"
      onClick={() => onOpen(step)}
      onMouseEnter={() => onHover(step.step_id)}
      onMouseLeave={() => onHover(null)}
      className={cn(
        "absolute z-10 flex items-center gap-1.5 text-left",
        fresh && "tl-pop"
      )}
      style={{
        left: rect.left,
        width: rect.width,
        top: rect.top,
        height: rect.height,
      }}
      title={
        idleCount
          ? `[idle ×${idleCount}] ${idleSpan} quiet`
          : `[${step.type}] ${step.preview}`
      }
    >
      <span
        className={cn(
          "shrink-0 rounded-sm",
          timelineColor(step.type),
          hot && palette.cellRing,
          flash && "tl-flash"
        )}
        style={{
          width: CELL,
          height: CELL,
          boxShadow: step.type === "idle" ? undefined : palette.cellGlow,
        }}
      />
      {!isCollapsed && (
        <span
          className={cn(
            "min-w-0 flex-1 text-[10px]",
            lines === 3
              ? "line-clamp-3 break-words leading-[13px]"
              : lines === 2
                ? "line-clamp-2 break-words leading-[13px]"
                : "truncate leading-none",
            step.type === "idle" ? palette.cellTextIdle : palette.cellText
          )}
        >
          {idleCount
            ? `idle ×${idleCount} · ${idleSpan}`
            : step.preview || step.type}
        </span>
      )}
    </button>
  );
});

interface RunChipProps {
  run: RunGroup;
  title: string;
  route: string | null;
  duration: string | null;
  iters: number;
  live: boolean;
  rect: TimelineRect;
  headerH: number;
  palette: CanvasPalette;
  onOpen: (runId: string) => void;
  onHover: (id: string | null) => void;
}

/** The run summary chip — a layer above the cells so the sticky chip
 * cleanly occludes nested steps while it floats. */
export const RunChip = memo(function RunChip({
  run,
  title,
  route,
  duration,
  iters,
  live,
  rect,
  headerH,
  palette,
  onOpen,
  onHover,
}: RunChipProps) {
  return (
    <div
      className="pointer-events-none absolute z-10 flex flex-col px-1.5 py-1"
      style={{
        left: rect.left,
        width: rect.width,
        top: rect.top,
        height: rect.height,
      }}
    >
      <button
        type="button"
        onClick={() => onOpen(run.run_id)}
        onMouseEnter={() => onHover(run.run_id)}
        onMouseLeave={() => onHover(null)}
        className={cn(
          "pointer-events-auto sticky flex w-full flex-col rounded border px-1.5 py-0.5 text-left",
          palette.chipBorder
        )}
        style={{
          top: headerH + 4,
          backgroundColor: palette.chipBg,
          boxShadow: palette.chipShadow,
        }}
        title={`[run] ${title}`}
      >
        <span
          className={cn(
            "line-clamp-2 w-full break-words text-[11px] italic leading-4",
            palette.chipTitle
          )}
          style={{ textShadow: palette.chipTitleGlow }}
        >
          {title}
        </span>
        <span
          className={cn(
            "w-full truncate font-mono text-[9px] leading-4",
            palette.chipMeta
          )}
        >
          {route && <span className={palette.chipRoute}>→ {route} · </span>}
          {run.status !== "done" && !run.ended_ts
            ? live
              ? "running"
              : "incomplete"
            : duration ?? "done"}
          {iters > 0 && <> · {iters} iter</>}
          {run.model && <> · {run.model.replace(/^claude-/, "")}</>}
        </span>
      </button>
    </div>
  );
});

interface LaneHeaderProps {
  lane: TimelineLane;
  left: number;
  width: number;
  headerH: number;
  isCollapsed: boolean;
  palette: CanvasPalette;
  onToggle: (id: string) => void;
  onHide: (id: string) => void;
  onMove: (id: string, dir: -1 | 1) => void;
}

/** One sticky lane-header cell — collapsed lanes shrink to an expand
 * button, expanded ones carry the name plus the hover-revealed controls
 * (move / collapse / hide). */
export const LaneHeader = memo(function LaneHeader({
  lane,
  left,
  width,
  headerH,
  isCollapsed,
  palette,
  onToggle,
  onHide,
  onMove,
}: LaneHeaderProps) {
  if (isCollapsed) {
    return (
      <button
        key={lane.id}
        type="button"
        onClick={() => onToggle(lane.id)}
        title={`expand ${lane.label}`}
        style={{ left, width, height: headerH }}
        className={cn(
          "absolute top-0 flex items-center justify-center rounded-t",
          palette.laneHead,
          palette.laneHeadHover
        )}
      >
        <ChevronsLeftRight className={cn("h-3 w-3 shrink-0", palette.collapseIcon)} />
      </button>
    );
  }
  const nameKind =
    lane.kind === "chat"
      ? ("chat" as const)
      : lane.kind === "shellm"
        ? ("shellm" as const)
        : ("default" as const);
  return (
    <div
      key={lane.id}
      style={{ left, width, height: headerH }}
      className={cn(
        "group absolute top-0 flex items-center gap-0.5 overflow-hidden rounded-t pl-2 pr-1",
        palette.laneHead
      )}
    >
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-xs uppercase tracking-widest",
          palette.laneName[nameKind]
        )}
        style={{ textShadow: palette.laneNameGlow[nameKind] }}
      >
        {lane.label}
      </span>
      <span className="flex shrink-0 items-center opacity-0 group-hover:opacity-100">
        <button
          type="button"
          onClick={() => onMove(lane.id, -1)}
          title="左移"
          aria-label={`左移 ${lane.label}`}
          className={cn("rounded p-0.5", palette.ctrl)}
        >
          <ChevronLeft className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => onMove(lane.id, 1)}
          title="右移"
          aria-label={`右移 ${lane.label}`}
          className={cn("rounded p-0.5", palette.ctrl)}
        >
          <ChevronRight className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => onToggle(lane.id)}
          title={`收起 ${lane.label}`}
          aria-label={`收起 ${lane.label}`}
          className={cn("rounded p-0.5", palette.ctrl)}
        >
          <ChevronsRightLeft className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => onHide(lane.id)}
          title={`隐藏 ${lane.label}`}
          aria-label={`隐藏 ${lane.label}`}
          className={cn("rounded p-0.5", palette.ctrl)}
        >
          <EyeOff className="h-3 w-3" />
        </button>
      </span>
    </div>
  );
});

interface PathPoint {
  x: number;
  y: number;
}

export interface RenderedEdgePath {
  edge: TimelineEdge;
  d: string;
  start: PathPoint;
  end: PathPoint;
}

interface EdgeLayerProps {
  paths: RenderedEdgePath[];
  width: number;
  height: number;
  hovered: string | null;
  palette: CanvasPalette;
}

/** SVG overlay for the causal edges. Re-renders when `hovered` flips (the
 * ghosting is global by design) but skips unrelated parent updates. */
export const EdgeLayer = memo(function EdgeLayer({
  paths,
  width,
  height,
  hovered,
  palette,
}: EdgeLayerProps) {
  return (
    <svg
      className="pointer-events-none absolute left-0 top-0"
      width={width}
      height={height}
    >
      {/* No arrowheads: time flows down, so direction is implied —
          endpoints are dots (source small, target larger). */}
      {paths.map(({ edge, d, start, end }) => {
        const s = edgeStyle(edge, hovered);
        const color = EDGE_STROKE[edge.kind];
        return (
          <g key={edge.id} opacity={s.opacity}>
            {s.halo && (
              <path
                d={d}
                fill="none"
                stroke={palette.canvasBg}
                strokeWidth={s.width + 2.5}
              />
            )}
            {/* neon under-glow */}
            <path
              d={d}
              fill="none"
              stroke={color}
              strokeWidth={s.width * 3 + 2}
              opacity={0.22}
            />
            <path d={d} fill="none" stroke={color} strokeWidth={s.width} />
            <circle cx={start.x} cy={start.y} r={s.width + 0.5} fill={color} />
            <circle cx={end.x} cy={end.y} r={s.width + 1.5} fill={color} />
          </g>
        );
      })}
    </svg>
  );
});

interface TimelineModalsProps {
  selected: TimelineSelection | null;
  /** applySelection(null) closes the detail modal; non-null re-points it
   * at a related item (the modal's internal navigation). */
  onSelect: (sel: TimelineSelection | null) => void;
  stepById: Map<string, NormalizedStep>;
  blockByRun: Map<string, TimelineBlock>;
  missing: { kind: "step" | "run"; id: string } | null;
  /** Traj context presence — a missing deeplink target outside the loaded
   * window fetches a one-off step modal when the context is available. */
  identityId: string | null;
  /** Clears the deeplink param (step or run) plus the missing marker. */
  onDismissMissing: (kind: "step" | "run") => void;
}

/** The modal cluster: selection detail plus the "deeplink target not in
 * the loaded window" fallbacks. */
export function TimelineModals({
  selected,
  onSelect,
  stepById,
  blockByRun,
  missing,
  identityId,
  onDismissMissing,
}: TimelineModalsProps) {
  return (
    <>
      {selected && (
        <TimelineDetailModal
          selected={selected}
          onClose={() => onSelect(null)}
          onSelect={onSelect}
          stepById={stepById}
          blockByRun={blockByRun}
        />
      )}

      {/* deeplink targets outside the loaded window */}
      {missing?.kind === "step" && identityId !== null && (
        <StepModal
          identityId={identityId}
          stepId={missing.id}
          onClose={() => onDismissMissing("step")}
        />
      )}
      {missing?.kind === "step" && identityId === null && (
        <Modal onClose={() => onDismissMissing("step")}>
          <div className="py-4 pr-8 text-sm text-muted-foreground">
            This step is not in the loaded part of the timeline. Use “load
            older” to page more history in, then open the link again.
          </div>
        </Modal>
      )}
      {missing?.kind === "run" && (
        <Modal onClose={() => onDismissMissing("run")}>
          <div className="py-4 pr-8 text-sm text-muted-foreground">
            This run is older than the loaded part of the timeline. Use
            “load older” to page more history in, then open the link again.
          </div>
        </Modal>
      )}
    </>
  );
}

interface FollowPillProps {
  pinned: boolean;
  onResume: () => void;
}

/** Floating follow-mode indicator / resume button (bottom-right corner). */
export function FollowPill({ pinned, onResume }: FollowPillProps) {
  if (pinned) {
    return (
      <div className="absolute bottom-3 right-3 z-30">
        <div className="flex items-center gap-1.5 rounded-full border bg-background/90 px-3 py-1.5 font-mono text-[11px] text-muted-foreground shadow-md backdrop-blur">
          <ArrowDownToLine className="h-3 w-3 text-green-500" />
          following
        </div>
      </div>
    );
  }
  return (
    <div className="absolute bottom-3 right-3 z-30">
      <button
        type="button"
        onClick={onResume}
        className="flex items-center gap-1.5 rounded-full border bg-background/90 px-3 py-1.5 font-mono text-[11px] shadow-md backdrop-blur hover:bg-accent"
      >
        <Pause className="h-3 w-3 text-amber-500" />
        paused · resume
      </button>
    </div>
  );
}
