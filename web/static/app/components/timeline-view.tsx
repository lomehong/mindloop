// Timeline tab: swimlane view of a mind log. Time runs down; each writer
// gets a lane; runs are summary blocks in the launcher's lane; exact causal
// edges (trigger/dispatch/assoc/merge) are drawn as an SVG overlay.
//
// Edge routing is orthogonal through dedicated inter-lane gutters: an edge
// leaves its source downward, runs along the source row's bottom boundary
// into the gutter beside the source lane, travels vertically inside the
// gutter, then horizontally along the target's own row. Because ordinal
// rows hold exactly one event each, those horizontal runs cross only empty
// lane space — edges never strike through text by construction.
//
// Visibility is tiered: trigger→run edges (the structural story, one per
// run) are always on; dispatch/assoc/merge edges rest as faint ghosts and
// pop to full strength when an attached cell or block is hovered.
//
// Lanes can be collapsed (narrow square-strip), hidden, and reordered; all
// three are URL-persisted (?collapsed= / ?hidden= / ?laneorder=).

import { EyeOff } from "lucide-react";
import { useTheme } from "next-themes";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  blockRoute,
  blockTitle,
  roundedPath,
} from "~/components/timeline-geometry";
import {
  CELL,
  CELL_PAD,
  COLLAPSED_W,
  DARK_PALETTE,
  LANE_GAP,
  LIGHT_PALETTE,
  ROW_CLICK_H,
  BLOCK_INSET,
  NEST_PAD,
} from "~/components/timeline-palette";
import {
  EdgeLayer,
  FollowPill,
  LaneHeader,
  RunBlockButton,
  RunChip,
  StepCellButton,
  TimelineModals,
  useHoverGhost,
} from "~/components/timeline-parts";
import type { TimelineSelection } from "~/components/timeline-detail";
import { useTrajContext } from "~/lib/traj-context";
import { formatDuration } from "~/lib/format";
import {
  GUTTER_W,
  HEADER_H,
  LANE_W,
  MID_ROW_H,
  MONO_LANE_W,
  TALL_ROW_H,
  rowCenterY,
  type TimelineBlock,
  type TimelineCell,
  type TimelineLane,
  type TimelineLayout,
} from "~/lib/timeline-model";
import type { NormalizedStep } from "~/lib/types";
import { cn } from "~/lib/utils";

export function TimelineView({
  layout,
  live,
  openStep,
}: {
  layout: TimelineLayout;
  live: boolean;
  /** Externally requested step detail (e.g. a search hit) — a fresh
   * wrapper object per request so repeat clicks reopen the modal. */
  openStep?: { step: NormalizedStep } | null;
}) {
  const traj = useTrajContext();
  // SPA, no SSR: resolvedTheme starts undefined; render dark until mounted.
  const { resolvedTheme } = useTheme();
  const palette = resolvedTheme === "light" ? LIGHT_PALETTE : DARK_PALETTE;
  const { hovered, setHover } = useHoverGhost();
  const [selected, setSelected] = useState<TimelineSelection | null>(null);

  // Deeplinks: ?step= / ?run= mirror the open detail modal, so the address
  // bar is always a shareable link to what's on screen. The params present
  // at mount are resolved once the layout is up (below).
  const [stepParam, setStepParam] = useQueryState("step", parseAsString);
  const [runParam, setRunParam] = useQueryState("run", parseAsString);
  const [flashId, setFlashId] = useState<string | null>(null);
  const [missing, setMissing] = useState<{
    kind: "step" | "run";
    id: string;
  } | null>(null);
  const pendingDeeplink = useRef<{
    step: string | null;
    run: string | null;
  } | null>({ step: stepParam, run: runParam });
  // The params the current selection already reflects. applySelection and
  // the resolver write it; the reconcile effect below reads it so their own
  // URL echoes don't re-trigger a resolve.
  const appliedParams = useRef<{ step: string | null; run: string | null }>({
    step: stepParam,
    run: runParam,
  });

  const applySelection = useCallback(
    (sel: TimelineSelection | null) => {
      const step = sel?.kind === "step" ? sel.step.step_id : null;
      const run = sel?.kind === "run" ? sel.block.run.run_id : null;
      appliedParams.current = { step, run };
      setSelected(sel);
      void setStepParam(step);
      void setRunParam(run);
    },
    [setStepParam, setRunParam]
  );

  useEffect(() => {
    if (openStep) applySelection({ kind: "step", step: openStep.step });
  }, [openStep, applySelection]);

  // Stable handlers for the memoized leaf parts: identical identities let
  // React.memo skip untouched cells/blocks on hover flips and poll ticks.
  const openStepDetail = useCallback(
    (step: NormalizedStep) => applySelection({ kind: "step", step }),
    [applySelection]
  );
  // openRun resolves the block through a ref (kept current further down,
  // right after blockById is built) so the callback never needs the
  // per-poll map in its deps.
  const blockByIdRef = useRef<Map<string, TimelineBlock> | null>(null);
  const openRun = useCallback((runId: string) => {
    const block = blockByIdRef.current?.get(runId);
    if (block) applySelection({ kind: "run", block });
  }, [applySelection]);
  const dismissMissing = useCallback(
    (kind: "step" | "run") => {
      setMissing(null);
      void (kind === "step" ? setStepParam(null) : setRunParam(null));
    },
    [setStepParam, setRunParam]
  );

  // Lane display state (all URL-persisted, comma-separated lane ids)
  const [collapsedParam, setCollapsedParam] = useQueryState(
    "collapsed",
    parseAsString.withDefault("")
  );
  const [hiddenParam, setHiddenParam] = useQueryState(
    "hidden",
    parseAsString.withDefault("")
  );
  const [orderParam, setOrderParam] = useQueryState(
    "laneorder",
    parseAsString.withDefault("")
  );
  const collapsed = useMemo(
    () => new Set(collapsedParam.split(",").filter(Boolean)),
    [collapsedParam]
  );
  const hidden = useMemo(
    () => new Set(hiddenParam.split(",").filter(Boolean)),
    [hiddenParam]
  );

  // Lane header controls — memoized LaneHeader consumes these, so keep
  // their identities stable across renders that don't touch lane state.
  const toggleLane = useCallback(
    (id: string) => {
      const next = new Set(collapsed);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      setCollapsedParam([...next].join(",") || null);
    },
    [collapsed, setCollapsedParam]
  );
  const hideLane = useCallback(
    (id: string) => {
      setHiddenParam([...new Set([...hidden, id])].join(","));
    },
    [hidden, setHiddenParam]
  );
  const unhideLane = (id: string) => {
    const next = new Set(hidden);
    next.delete(id);
    setHiddenParam([...next].join(",") || null);
  };

  // Visible lanes in display order; map original lane index -> display index
  const displayLanes: TimelineLane[] = useMemo(() => {
    const ids = layout.lanes.map((l) => l.id);
    const pref = orderParam.split(",").filter((id) => ids.includes(id));
    const ordered = [...pref, ...ids.filter((id) => !pref.includes(id))];
    return ordered
      .filter((id) => !hidden.has(id))
      .map((id) => layout.lanes.find((l) => l.id === id)!);
  }, [layout.lanes, orderParam, hidden]);

  const dispIdxById = useMemo(
    () => new Map(displayLanes.map((l, i) => [l.id, i])),
    [displayLanes]
  );
  const disp = (origLane: number): number | undefined =>
    dispIdxById.get(layout.lanes[origLane].id);

  const moveLane = useCallback(
    (id: string, dir: -1 | 1) => {
      const ids = displayLanes.map((l) => l.id);
      const i = ids.indexOf(id);
      const j = i + dir;
      if (i < 0 || j < 0 || j >= ids.length) return;
      [ids[i], ids[j]] = [ids[j], ids[i]];
      setOrderParam(ids.join(","));
    },
    [displayLanes, setOrderParam]
  );

  // Lane x-geometry (display order): a routing gutter precedes every lane
  const { laneX, laneW, width } = useMemo(() => {
    const laneX: number[] = [];
    const laneW: number[] = [];
    let x = GUTTER_W;
    for (const lane of displayLanes) {
      x += LANE_GAP;
      laneX.push(x);
      const w = collapsed.has(lane.id)
        ? COLLAPSED_W
        : lane.id === "inner_monologue"
          ? MONO_LANE_W
          : LANE_W;
      laneW.push(w);
      x += w;
    }
    return { laneX, laneW, width: x + LANE_GAP };
  }, [displayLanes, collapsed]);

  const bodyHeight = layout.totalHeight;

  // New arrivals pop in (a thought arriving). Track what we've already
  // seen so the initial load doesn't animate everything at once. The
  // memo stays pure — it only READS the ref; the ref is written back in
  // an effect, so StrictMode's double render sees a consistent diff.
  const seenRef = useRef<Set<string> | null>(null);
  const seen = useMemo(() => {
    const ids = new Set<string>();
    for (const cell of layout.cells) ids.add(cell.step.step_id);
    for (const block of layout.blocks) ids.add(block.run.run_id);
    return ids;
  }, [layout]);
  const freshIds = useMemo(() => {
    const prev = seenRef.current;
    if (prev === null) return new Set<string>();
    return new Set([...seen].filter((id) => !prev.has(id)));
  }, [seen]);
  useEffect(() => {
    seenRef.current = seen;
  }, [seen]);

  // Follow mode: the timeline scrolls inside its own container (so the lane
  // header can stick); while pinned to the bottom, new rows scroll into view.
  const scrollRef = useRef<HTMLDivElement>(null);
  // Arriving via a deeplink starts unpinned so follow mode doesn't yank
  // the view to the bottom before the target is seen.
  const [pinned, setPinned] = useState(
    () => !(pendingDeeplink.current?.step || pendingDeeplink.current?.run)
  );
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      setPinned(el.scrollHeight - el.scrollTop - el.clientHeight < 60);
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);
  useEffect(() => {
    const el = scrollRef.current;
    if (el && pinned) el.scrollTop = el.scrollHeight;
  }, [bodyHeight, pinned]);
  const resumeFollow = useCallback(() => {
    const el = scrollRef.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, []);

  // --- geometry helpers (display coordinates; null when the lane is hidden) --

  const cellSquareX = (cell: TimelineCell): number | null => {
    const d = disp(cell.lane);
    if (d === undefined) return null;
    if (collapsed.has(layout.lanes[cell.lane].id)) {
      return laneX[d] + laneW[d] / 2;
    }
    if (cell.inBlock) {
      return laneX[d] + BLOCK_INSET + NEST_PAD + CELL / 2;
    }
    return laneX[d] + CELL_PAD + CELL / 2;
  };

  const blockRect = (block: TimelineBlock) => {
    const d = disp(block.lane);
    if (d === undefined) return null;
    const isCollapsed = collapsed.has(layout.lanes[block.lane].id);
    const left = laneX[d] + (isCollapsed ? 4 : BLOCK_INSET);
    const right = laneX[d] + laneW[d] - (isCollapsed ? 4 : BLOCK_INSET);
    const top = layout.rowY[block.startRow] + 2;
    const bottom = layout.rowY[block.endRow] + layout.rowH[block.endRow] - 2;
    return { left, right, top, bottom, collapsed: isCollapsed, d };
  };

  const cellById = useMemo(() => {
    const m = new Map<string, TimelineCell>();
    for (const cell of layout.cells) m.set(cell.step.step_id, cell);
    return m;
  }, [layout.cells]);
  const blockById = useMemo(() => {
    const m = new Map<string, TimelineBlock>();
    for (const block of layout.blocks) m.set(block.run.run_id, block);
    return m;
  }, [layout.blocks]);
  useEffect(() => {
    blockByIdRef.current = blockById;
  }, [blockById]);

  // Run route labels for the summary chips — one O(cells) pass per layout
  // instead of per chip per render (was O(blocks × cells) each render).
  const routeByRunId = useMemo(() => {
    const m = new Map<string, string | null>();
    for (const block of layout.blocks) {
      m.set(block.run.run_id, blockRoute(block, layout.cells));
    }
    return m;
  }, [layout.blocks, layout.cells]);

  // Every step (cells + run members), for the modal's related-item lookups
  const stepById = useMemo(() => {
    const m = new Map<string, NormalizedStep>();
    for (const cell of layout.cells) m.set(cell.step.step_id, cell.step);
    for (const block of layout.blocks) {
      for (const member of block.members) m.set(member.step_id, member);
    }
    return m;
  }, [layout.cells, layout.blocks]);

  // --- deeplink resolution --------------------------------------------------

  // Find the linked element, scroll its row into view, pulse it, and open
  // its detail modal. A step outside the loaded window (or one that no
  // longer exists) falls back to a fetch-one modal, like an old search
  // hit — deliberately NOT auto-paging history in, which could mean tens
  // of MB on a grown mind log. Step wins when both params are set; the
  // losing param is cleared so the URL always names exactly one item.
  const resolveDeeplink = useCallback(
    (want: { step: string | null; run: string | null }) => {
      const scrollToRow = (row: number) => {
        const el = scrollRef.current;
        if (el) {
          el.scrollTop = Math.max(0, layout.rowY[row] - el.clientHeight / 3);
        }
      };

      if (want.step) {
        const step = stepById.get(want.step);
        if (!step) {
          appliedParams.current = { step: want.step, run: null };
          setSelected(null);
          setMissing({ kind: "step", id: want.step });
          void setRunParam(null);
          return;
        }
        // Run machinery steps have no cell of their own; land on their block.
        const cell = cellById.get(want.step);
        const runId = typeof step.raw.run_id === "string" ? step.raw.run_id : null;
        const block = !cell && runId ? blockById.get(runId) : undefined;
        const row = cell?.row ?? block?.startRow;
        if (row !== undefined) {
          scrollToRow(row);
          setFlashId(cell ? want.step : block!.run.run_id);
        }
        setMissing(null);
        applySelection({ kind: "step", step });
      } else if (want.run) {
        const block = blockById.get(want.run);
        if (!block) {
          appliedParams.current = { step: null, run: want.run };
          setSelected(null);
          setMissing({ kind: "run", id: want.run });
          void setStepParam(null);
          return;
        }
        scrollToRow(block.startRow);
        setFlashId(want.run);
        setMissing(null);
        applySelection({ kind: "run", block });
      }
    },
    [layout, cellById, blockById, stepById, applySelection, setStepParam, setRunParam]
  );

  // Mount-time pass: resolve the params present at arrival once a real
  // layout is up.
  useEffect(() => {
    const want = pendingDeeplink.current;
    if (!want) return;
    if (!want.step && !want.run) {
      pendingDeeplink.current = null;
      return;
    }
    // Nothing to match against yet: keep the deeplink pending so a
    // still-loading first layout doesn't produce a false "missing".
    if (layout.cells.length === 0 && layout.blocks.length === 0) return;
    pendingDeeplink.current = null;
    resolveDeeplink(want);
  }, [layout, resolveDeeplink]);

  // Reconcile the modal with URL changes after mount (Back/Forward, soft
  // navigation): re-run the resolve path when the params stop matching the
  // selection they last produced, and close everything when both clear.
  useEffect(() => {
    if (pendingDeeplink.current) return; // mount pass owns the first resolve
    const applied = appliedParams.current;
    if (stepParam === applied.step && runParam === applied.run) return;
    appliedParams.current = { step: stepParam, run: runParam };
    if (!stepParam && !runParam) {
      setSelected(null);
      setMissing(null);
      return;
    }
    resolveDeeplink({ step: stepParam, run: runParam });
  }, [stepParam, runParam, resolveDeeplink]);

  useEffect(() => {
    if (!flashId) return;
    const timer = setTimeout(() => setFlashId(null), 3500);
    return () => clearTimeout(timer);
  }, [flashId]);

  // --- orthogonal edge routing ----------------------------------------------

  const edgePaths = useMemo(() => {
    const dispLaneOf = (id: string): number | undefined => {
      const lane = cellById.get(id)?.lane ?? blockById.get(id)?.lane;
      return lane === undefined ? undefined : disp(lane);
    };

    const rowBottom = (row: number) => layout.rowY[row] + layout.rowH[row];

    const paths: {
      edge: (typeof layout.edges)[number];
      d: string;
      start: { x: number; y: number };
      end: { x: number; y: number };
    }[] = [];
    for (const edge of layout.edges) {
      const srcLane = dispLaneOf(edge.fromId);
      const tgtLane = dispLaneOf(edge.toId);
      if (srcLane === undefined || tgtLane === undefined) continue; // hidden

      // gutter beside the source lane, on the side facing the target
      const goRight = tgtLane > srcLane;
      const gx = goRight
        ? laneX[srcLane] + laneW[srcLane] + LANE_GAP / 2
        : laneX[srcLane] - LANE_GAP / 2;

      const pts: { x: number; y: number }[] = [];

      // -- departure --
      const srcCell = cellById.get(edge.fromId);
      if (srcCell) {
        // out of the square's bottom, along the row boundary, into the gutter
        const sx = cellSquareX(srcCell)!;
        const sy = rowCenterY(layout, srcCell.row);
        const sBound = rowBottom(srcCell.row);
        pts.push({ x: sx, y: sy + CELL / 2 + 1 }, { x: sx, y: sBound }, { x: gx, y: sBound });
      } else {
        const block = blockById.get(edge.fromId)!;
        const r = blockRect(block)!;
        const by = rowCenterY(layout, block.startRow);
        pts.push({ x: goRight ? r.right : r.left, y: by }, { x: gx, y: by });
      }

      // -- arrival --
      const tgtCell = cellById.get(edge.toId);
      if (tgtCell) {
        const tx = cellSquareX(tgtCell)!;
        const ty = rowCenterY(layout, tgtCell.row);
        if (gx <= tx) {
          // approach from the left, straight into the square's side
          pts.push({ x: gx, y: ty }, { x: tx - CELL / 2 - 3, y: ty });
        } else {
          // approach from the right: run along the boundary above the
          // target's row, then drop into the square's top — never across
          // the row's own text
          const tBound = layout.rowY[tgtCell.row];
          pts.push({ x: gx, y: tBound }, { x: tx, y: tBound }, { x: tx, y: ty - CELL / 2 - 2 });
        }
      } else {
        const block = blockById.get(edge.toId)!;
        const r = blockRect(block)!;
        const by = rowCenterY(layout, block.startRow);
        const arriveX = gx <= (r.left + r.right) / 2 ? r.left - 2 : r.right + 2;
        pts.push({ x: gx, y: by }, { x: arriveX, y: by });
      }

      paths.push({ edge, d: roundedPath(pts), start: pts[0], end: pts[pts.length - 1] });
    }
    return paths;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layout, cellById, blockById, laneX, laneW, collapsed, dispIdxById]);

  return (
    <div className="relative">
      {hidden.size > 0 && (
        <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
          <span className="font-mono text-[10px] text-muted-foreground">hidden:</span>
          {[...hidden].map((id) => (
            <button
              key={id}
              type="button"
              onClick={() => unhideLane(id)}
              title={`show ${id}`}
              className="flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <EyeOff className="h-2.5 w-2.5" />
              {id}
            </button>
          ))}
        </div>
      )}
      <div
        ref={scrollRef}
        className={cn("overflow-auto rounded-lg border", palette.frame)}
        style={{ maxHeight: "calc(100vh - 210px)", backgroundColor: palette.canvasBg }}
      >
        <div
          className="relative"
          style={{
            width,
            minWidth: "100%",
            backgroundImage: palette.canvasGrid,
            backgroundSize: "28px 28px",
          }}
        >
          {/* sticky lane headers */}
          <div
            className={cn("sticky top-0 z-20 border-b backdrop-blur", palette.headerBorder)}
            style={{ height: HEADER_H, width, backgroundColor: palette.headerBg }}
          >
            {displayLanes.map((lane, i) => (
              <LaneHeader
                key={lane.id}
                lane={lane}
                left={laneX[i]}
                width={laneW[i]}
                headerH={HEADER_H}
                isCollapsed={collapsed.has(lane.id)}
                palette={palette}
                onToggle={toggleLane}
                onHide={hideLane}
                onMove={moveLane}
              />
            ))}
          </div>

          {/* body */}
          <div className="relative" style={{ height: bodyHeight, width }}>
            {/* lane columns — fills only, no borders; the gutters between
                them are open routing channels */}
            {displayLanes.map((lane, i) => (
              <div
                key={lane.id}
                className={cn(
                  "absolute top-0 rounded-b",
                  collapsed.has(lane.id) ? palette.laneFillCollapsed : palette.laneFill
                )}
                style={{ left: laneX[i], width: laneW[i], height: bodyHeight }}
              />
            ))}

            {/* wall-clock gutter */}
            {layout.rowClock.map((label, row) =>
              label ? (
                <div
                  key={row}
                  className={cn(
                    "absolute pr-2 text-right font-mono text-[9px] leading-none",
                    palette.clock
                  )}
                  style={{ left: 0, width: GUTTER_W, top: rowCenterY(layout, row) - 4 }}
                >
                  {label}
                </div>
              ) : null
            )}

            {/* gap dividers */}
            {layout.gaps.map((gap) => (
              <div
                key={gap.row}
                className="absolute flex items-center gap-2 px-3"
                style={{
                  left: GUTTER_W,
                  width: width - GUTTER_W,
                  top: layout.rowY[gap.row],
                  height: layout.rowH[gap.row],
                }}
              >
                <span className={cn("h-px flex-1 border-t border-dashed", palette.gapBorder)} />
                <span
                  className={cn("font-mono text-[10px]", palette.gapText)}
                  style={{ textShadow: palette.gapGlow }}
                >
                  {gap.label}
                </span>
                <span className={cn("h-px flex-1 border-t border-dashed", palette.gapBorder)} />
              </div>
            ))}

            {/* edges */}
            <EdgeLayer
              paths={edgePaths}
              width={width}
              height={bodyHeight}
              hovered={hovered}
              palette={palette}
            />

            {/* run blocks (background + border; the summary chip is painted
                in a separate layer above the cells, see below) */}
            {layout.blocks.map((block) => {
              const r = blockRect(block);
              if (!r) return null; // hidden lane
              return (
                <RunBlockButton
                  key={block.run.run_id}
                  run={block.run}
                  title={blockTitle(block)}
                  rect={{
                    left: r.left,
                    width: r.right - r.left,
                    top: r.top,
                    height: Math.max(r.bottom - r.top, 24),
                  }}
                  running={block.open && live}
                  hot={hovered === block.run.run_id}
                  fresh={freshIds.has(block.run.run_id)}
                  flash={flashId === block.run.run_id}
                  palette={palette}
                  onOpen={openRun}
                  onHover={setHover}
                />
              );
            })}

            {/* step cells */}
            {layout.cells.map((cell) => {
              const squareX = cellSquareX(cell);
              if (squareX === null) return null; // hidden lane
              const d = disp(cell.lane)!;
              const y = rowCenterY(layout, cell.row);
              const isCollapsed = collapsed.has(layout.lanes[cell.lane].id);
              // preview depth follows the row height the model assigned
              const lines =
                layout.rowH[cell.row] >= TALL_ROW_H
                  ? 3
                  : layout.rowH[cell.row] >= MID_ROW_H
                    ? 2
                    : 1;
              const clickH = lines === 3 ? 46 : lines === 2 ? 34 : ROW_CLICK_H;
              return (
                <StepCellButton
                  key={cell.step.step_id}
                  step={cell.step}
                  idleCount={cell.idleCount}
                  idleSpan={cell.idleSpan}
                  rect={
                    isCollapsed
                      ? {
                          left: squareX - CELL / 2 - 2,
                          width: CELL + 4,
                          top: y - clickH / 2,
                          height: clickH,
                        }
                      : {
                          left: squareX - CELL / 2,
                          width:
                            laneX[d] +
                            laneW[d] -
                            (squareX - CELL / 2) -
                            (cell.inBlock ? BLOCK_INSET + NEST_PAD : CELL_PAD),
                          top: y - clickH / 2,
                          height: clickH,
                        }
                  }
                  lines={lines}
                  isCollapsed={isCollapsed}
                  hot={hovered === cell.step.step_id}
                  fresh={freshIds.has(cell.step.step_id)}
                  flash={flashId === cell.step.step_id}
                  palette={palette}
                  onOpen={openStepDetail}
                  onHover={setHover}
                />
              );
            })}

            {/* run summary chips — a layer above the cells so the sticky
                chip cleanly occludes nested steps while it floats, instead
                of z-fighting their text */}
            {layout.blocks.map((block) => {
              const r = blockRect(block);
              if (!r || r.collapsed) return null;
              const iters = block.members.filter((m) => m.type === "shell-output").length;
              const duration = formatDuration(block.run.started_ts, block.run.ended_ts);
              const route = routeByRunId.get(block.run.run_id) ?? null;
              return (
                <RunChip
                  key={`chip-${block.run.run_id}`}
                  run={block.run}
                  title={blockTitle(block)}
                  route={route}
                  duration={duration}
                  iters={iters}
                  live={live}
                  rect={{
                    left: r.left,
                    width: r.right - r.left,
                    top: r.top,
                    height: Math.max(r.bottom - r.top, 24),
                  }}
                  headerH={HEADER_H}
                  palette={palette}
                  onOpen={openRun}
                  onHover={setHover}
                />
              );
            })}

            {/* CRT scanlines + horizon glow (pure decoration) */}
            <div
              className="pointer-events-none absolute inset-0 z-[12]"
              style={{
                backgroundImage: palette.scanlines,
                opacity: 0.18,
              }}
            />
            {palette.horizonGlow && (
              <div
                className="pointer-events-none absolute inset-x-0 bottom-0 z-[1] h-64"
                style={{ background: palette.horizonGlow }}
              />
            )}
          </div>
        </div>
      </div>

      {live && <FollowPill pinned={pinned} onResume={resumeFollow} />}

      <TimelineModals
        selected={selected}
        onSelect={applySelection}
        stepById={stepById}
        blockByRun={blockById}
        missing={missing}
        identityId={traj?.identityId ?? null}
        onDismissMissing={dismissMissing}
      />
    </div>
  );
}
