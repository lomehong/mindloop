// Timeline skin: two hand-tuned palettes (dark "deep-space neon" and light
// "drafting paper") plus the geometry constants shared by the timeline
// renderer. Everything inside the timeline canvas uses these palette
// entries, not the app's theme tokens — the canvas is a deliberate
// retro-future island; geometry constants pair with lib/timeline-model.ts.

import type { EdgeKind } from "~/lib/timeline-model";

export const EDGE_STROKE: Record<EdgeKind, string> = {
  trigger: "#f59e0b", // amber-500 — step that caused a run
  dispatch: "#38bdf8", // sky-400 — step that woke a thinker
  assoc: "#60a5fa", // blue-400 — run -> step it wrote
  merge: "#e879f9", // fuchsia-400 — fork -> merge write-back
};

export interface CanvasPalette {
  canvasBg: string;
  canvasGrid: string;
  frame: string; // outer container border + shadow classes
  headerBg: string;
  headerBorder: string;
  laneHead: string;
  laneHeadHover: string;
  collapseIcon: string;
  laneName: { chat: string; shellm: string; default: string };
  laneNameGlow: { chat?: string; shellm?: string; default?: string };
  ctrl: string; // lane header hover buttons
  laneFill: string;
  laneFillCollapsed: string;
  clock: string;
  gapBorder: string;
  gapText: string;
  gapGlow?: string;
  block: string;
  blockRing: string;
  blockShadow?: string;
  cellRing: string;
  cellGlow?: string; // square halo for non-idle cells
  cellText: string;
  cellTextIdle: string;
  chipBorder: string;
  chipBg: string;
  chipShadow?: string;
  chipTitle: string;
  chipTitleGlow?: string;
  chipMeta: string;
  chipRoute: string;
  scanlines: string;
  horizonGlow: string | null;
}

export const DARK_PALETTE: CanvasPalette = {
  canvasBg: "#0a0420",
  canvasGrid:
    "linear-gradient(rgba(34,211,238,0.055) 1px, transparent 1px), linear-gradient(90deg, rgba(34,211,238,0.055) 1px, transparent 1px)",
  frame: "border-cyan-400/25 shadow-[0_0_24px_rgba(34,211,238,0.12)]",
  headerBg: "rgba(13,5,38,0.92)",
  headerBorder: "border-cyan-400/25",
  laneHead: "bg-cyan-300/[0.06]",
  laneHeadHover: "hover:bg-cyan-300/[0.14]",
  collapseIcon: "text-cyan-400/70",
  laneName: {
    chat: "text-fuchsia-300",
    shellm: "text-purple-300/80",
    default: "text-cyan-300",
  },
  laneNameGlow: {
    chat: "0 0 10px rgba(240,171,252,0.55)",
    shellm: "0 0 10px rgba(103,232,249,0.55)",
    default: "0 0 10px rgba(103,232,249,0.55)",
  },
  ctrl: "text-cyan-400/60 hover:bg-cyan-300/10 hover:text-cyan-200",
  laneFill: "bg-cyan-300/[0.045]",
  laneFillCollapsed: "bg-cyan-300/[0.09]",
  clock: "text-cyan-400/50",
  gapBorder: "border-fuchsia-400/30",
  gapText: "text-fuchsia-300/70",
  gapGlow: "0 0 8px rgba(240,171,252,0.4)",
  block: "border-cyan-400/50 bg-cyan-400/[0.06] hover:bg-cyan-400/[0.12]",
  blockRing: "ring-2 ring-cyan-300/70",
  blockShadow:
    "0 0 10px rgba(34,211,238,0.22), inset 0 0 22px rgba(34,211,238,0.05)",
  cellRing: "ring-2 ring-cyan-200/60",
  cellGlow: "0 0 6px rgba(255,255,255,0.28)",
  cellText: "text-slate-300/90",
  cellTextIdle: "text-slate-500/60",
  chipBorder: "border-cyan-400/40",
  chipBg: "#120b32",
  chipShadow: "0 0 8px rgba(34,211,238,0.25)",
  chipTitle: "text-cyan-100",
  chipTitleGlow: "0 0 6px rgba(103,232,249,0.4)",
  chipMeta: "text-cyan-300/60",
  chipRoute: "text-fuchsia-300/90",
  scanlines:
    "repeating-linear-gradient(0deg, rgba(0,0,0,0.22) 0px, rgba(0,0,0,0.22) 1px, transparent 1px, transparent 3px)",
  horizonGlow:
    "radial-gradient(ellipse 80% 100% at 50% 115%, rgba(217,70,239,0.14), transparent 65%)",
};

export const LIGHT_PALETTE: CanvasPalette = {
  canvasBg: "#f7f9fd",
  canvasGrid:
    "linear-gradient(rgba(8,51,68,0.08) 1px, transparent 1px), linear-gradient(90deg, rgba(8,51,68,0.08) 1px, transparent 1px)",
  frame: "border-cyan-900/15 shadow-[0_1px_3px_rgba(8,51,68,0.08)]",
  headerBg: "rgba(255,255,255,0.92)",
  headerBorder: "border-cyan-900/15",
  laneHead: "bg-cyan-900/[0.05]",
  laneHeadHover: "hover:bg-cyan-900/[0.12]",
  collapseIcon: "text-cyan-700/70",
  laneName: {
    chat: "text-fuchsia-600",
    shellm: "text-purple-600/90",
    default: "text-cyan-700",
  },
  laneNameGlow: {},
  ctrl: "text-cyan-700/60 hover:bg-cyan-900/10 hover:text-cyan-900",
  laneFill: "bg-cyan-900/[0.04]",
  laneFillCollapsed: "bg-cyan-900/[0.08]",
  clock: "text-cyan-800/60",
  gapBorder: "border-fuchsia-500/40",
  gapText: "text-fuchsia-600/80",
  block: "border-cyan-600/40 bg-cyan-500/[0.07] hover:bg-cyan-500/[0.14]",
  blockRing: "ring-2 ring-cyan-600/60",
  blockShadow: "0 1px 2px rgba(8,51,68,0.10)",
  cellRing: "ring-2 ring-cyan-600/50",
  cellText: "text-zinc-700",
  cellTextIdle: "text-zinc-500",
  chipBorder: "border-cyan-600/40",
  chipBg: "#ffffff",
  chipShadow: "0 1px 3px rgba(8,51,68,0.12)",
  chipTitle: "text-cyan-950",
  chipMeta: "text-zinc-500",
  chipRoute: "text-fuchsia-600",
  scanlines:
    "repeating-linear-gradient(0deg, rgba(255,255,255,0.5) 0px, rgba(255,255,255,0.5) 1px, transparent 1px, transparent 3px)",
  horizonGlow: null,
};

// Canvas geometry (px) — consumers: timeline-view renderer and the parts
// extracted from it. Kept next to the palettes so the canvas stays one unit.
export const CELL = 12; // square size
export const ROW_CLICK_H = 20; // click target taller than the square
export const COLLAPSED_W = 40;
export const LANE_GAP = 24; // inter-lane gutter, reserved for edge routing
export const CELL_PAD = 8; // lane-edge padding for cells/blocks
export const BLOCK_INSET = CELL_PAD - 2; // block edge inset from the lane edge
export const NEST_PAD = 10; // nested steps align with the block's inner padding
