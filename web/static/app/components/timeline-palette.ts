// 泳道画布皮肤（docs/designs/ui-language.md §7 B）：格纸底 + 分类学色。
// 原"紫底实验室"岛已废弃——画布回归年轮 token，全部取值引用 app.css
// 的 CSS 变量（暗/亮由 html.dark 切换，此处单表不再分主题）。
// 几何常量与 lib/timeline-model.ts 配套。

import type { EdgeKind } from "~/lib/timeline-model";

/** 因果边四色（分类学内取色，见 lib/step-colors.ts）：
 * trigger 树脂=行动链起点 · dispatch 苔=思考链传导 ·
 * assoc 湖=运行产物被观察 · merge 梅=结构事件。 */
export const EDGE_STROKE: Record<EdgeKind, string> = {
  trigger: "var(--resin)",
  dispatch: "var(--moss)",
  assoc: "var(--lake)",
  merge: "var(--plum)",
};

export interface CanvasPalette {
  canvasBg: string;
  canvasGrid: string;
  frame: string; // outer container border classes
  headerBg: string;
  headerBorder: string;
  laneHead: string;
  laneHeadHover: string;
  collapseIcon: string;
  laneName: string; // lane label text color
  laneDot: Record<"chat" | "shellm" | "default", string>; // 泳道头圆点
  ctrl: string; // lane header hover buttons
  laneFill: string;
  laneFillCollapsed: string;
  clock: string;
  gapBorder: string;
  gapText: string;
  block: string;
  blockRing: string;
  cellRing: string;
  cellText: string;
  cellTextIdle: string;
  chipBorder: string;
  chipBg: string;
  chipTitle: string;
  chipMeta: string;
  chipRoute: string;
}

export const PALETTE: CanvasPalette = {
  canvasBg: "var(--surface)",
  canvasGrid:
    "linear-gradient(var(--grid) 1px, transparent 1px), linear-gradient(90deg, var(--grid) 1px, transparent 1px)",
  frame: "border-line",
  headerBg: "color-mix(in oklab, var(--surface) 92%, transparent)",
  headerBorder: "border-line",
  laneHead: "bg-transparent",
  laneHeadHover: "hover:bg-surface-2",
  collapseIcon: "text-faint",
  laneName: "text-foreground/80",
  laneDot: {
    chat: "bg-lake",
    shellm: "bg-resin",
    default: "bg-moss",
  },
  ctrl: "text-muted-foreground hover:bg-accent hover:text-foreground",
  laneFill: "border-l border-line",
  laneFillCollapsed: "border-l border-line bg-surface-2/50",
  clock: "text-faint",
  gapBorder: "border-line-strong",
  gapText: "text-faint",
  block: "border-resin/35 bg-resin/5 hover:bg-resin/10",
  blockRing: "ring-2 ring-resin/40",
  cellRing: "ring-2 ring-foreground/30",
  cellText: "text-foreground/85",
  cellTextIdle: "text-faint",
  chipBorder: "border-resin/40",
  chipBg: "var(--surface)",
  chipTitle: "font-note italic text-foreground/90",
  chipMeta: "text-faint",
  chipRoute: "text-plum",
};

// Canvas geometry (px) — consumers: timeline-view renderer and the parts
// extracted from it. Kept next to the palette so the canvas stays one unit.
export const CELL = 12; // square size
export const ROW_CLICK_H = 20; // click target taller than the square
export const COLLAPSED_W = 40;
export const LANE_GAP = 24; // inter-lane gutter, reserved for edge routing
export const CELL_PAD = 8; // lane-edge padding for cells/blocks
export const BLOCK_INSET = CELL_PAD - 2; // block edge inset from the lane edge
export const NEST_PAD = 10; // nested steps align with the block's inner padding
