// Pure helpers for the timeline renderer: run-block titles/routes, the
// tiered edge-visibility rule, and the rounded orthogonal SVG path used by
// the edge overlay. No React, no DOM.

import type {
  EdgeKind,
  TimelineBlock,
  TimelineCell,
} from "~/lib/timeline-model";

export interface EdgeVisual {
  opacity: number;
  width: number;
  halo: boolean;
}

/** Tiered visibility: triggers always on; the rest ghost until hovered. */
export function edgeStyle(
  edge: { fromId: string; toId: string; kind: EdgeKind },
  hovered: string | null
): EdgeVisual {
  const hot =
    hovered !== null && (edge.fromId === hovered || edge.toId === hovered);
  if (hot) return { opacity: 1, width: 2.2, halo: true };
  if (hovered !== null)
    return {
      opacity: edge.kind === "trigger" ? 0.12 : 0.05,
      width: 1.25,
      halo: false,
    };
  if (edge.kind === "trigger") return { opacity: 0.7, width: 1.5, halo: true };
  return { opacity: 0.14, width: 1.25, halo: false };
}

export function blockTitle(block: TimelineBlock): string {
  if (block.run.tldr) return block.run.tldr;
  const cmd = block.run.command;
  const idx = cmd.lastIndexOf("ACTION:");
  if (idx >= 0) return cmd.slice(idx + 7).replace(/\s+/g, " ").trim();
  // The final response beats the prompt: reuse-traj runs (the monolith
  // router) share one boilerplate prompt every wakeup, so the outcome is
  // the only text that distinguishes one run from the next.
  const final = block.members.filter((m) => m.type === "final").at(-1);
  if (final?.preview) return final.preview;
  const prompt = block.members.find((m) => m.type === "prompt");
  if (prompt?.preview) return prompt.preview;
  return cmd.replace(/\s+/g, " ").trim() || "shellm run";
}

/* What the run DID, from the steps it wrote back to the mind log — the
 * monolith's route choice (reply/act/think/idle) in one word. Priority
 * order: an outward message outranks a mere observation, etc. */
const ROUTE_BY_WROTE: [string, string][] = [
  ["message", "reply"],
  ["observation", "act"],
  ["thought", "think"],
  ["idle", "idle"],
];

export function blockRoute(
  block: TimelineBlock,
  cells: TimelineCell[]
): string | null {
  const wrote = new Set<string>();
  for (const cell of cells) {
    if (cell.step.raw.run_id === block.run.run_id) wrote.add(cell.step.type);
  }
  for (const [type, label] of ROUTE_BY_WROTE) {
    if (wrote.has(type)) return label;
  }
  return null;
}

/** Orthogonal polyline with rounded corners. */
export function roundedPath(
  points: { x: number; y: number }[],
  r = 7
): string {
  if (points.length < 2) return "";
  let d = `M ${points[0].x} ${points[0].y}`;
  for (let i = 1; i < points.length - 1; i++) {
    const p = points[i - 1];
    const c = points[i];
    const n = points[i + 1];
    const inLen = Math.hypot(c.x - p.x, c.y - p.y);
    const outLen = Math.hypot(n.x - c.x, n.y - c.y);
    const rr = Math.min(r, inLen / 2, outLen / 2);
    if (rr < 0.5) {
      d += ` L ${c.x} ${c.y}`;
      continue;
    }
    const inU = { x: (c.x - p.x) / inLen, y: (c.y - p.y) / inLen };
    const outU = { x: (n.x - c.x) / outLen, y: (n.y - c.y) / outLen };
    d += ` L ${c.x - inU.x * rr} ${c.y - inU.y * rr}`;
    d += ` Q ${c.x} ${c.y} ${c.x + outU.x * rr} ${c.y + outU.y * rr}`;
  }
  const last = points[points.length - 1];
  d += ` L ${last.x} ${last.y}`;
  return d;
}
