// Layout model regression: run-block extent (binary search over rowMs) and
// the derived geometry invariants. The extent logic replaced a linear scan;
// these cases pin its observable behavior, including gap-divider rows
// (rowMs = 0) between dated rows.

import { describe, expect, it } from "vitest";

import { buildTimeline, rowCenterY } from "~/lib/timeline-model";
import type { Mindlog, NormalizedStep, RunGroup } from "~/lib/types";

let seq = 0;

function step(
  ts: string,
  overrides: Partial<NormalizedStep> = {}
): NormalizedStep {
  seq += 1;
  return {
    step_id: `s${seq}`,
    ts,
    type: "thought",
    source: null,
    preview: `step ${seq}`,
    raw: {},
    run_id: null,
    ...overrides,
  };
}

function run(
  run_id: string,
  header: NormalizedStep,
  memberSteps: NormalizedStep[],
  overrides: Partial<RunGroup> = {}
): RunGroup {
  return {
    run_id,
    trigger_step_id: null,
    launched_by: "actor",
    // The shellm-run header step anchors the block: buildTimeline only
    // materializes a block for a step that is a member of a run.
    step_ids: [header.step_id, ...memberSteps.map((s) => s.step_id)],
    started_ts: header.ts,
    ended_ts: null,
    status: "running",
    command: "ACTION: do the thing",
    model: null,
    tldr: null,
    last_touch: 0,
    ...overrides,
  };
}

function build(steps: NormalizedStep[], runs: RunGroup[]) {
  const mindlog: Pick<Mindlog, "steps" | "runs"> = { steps, runs };
  return buildTimeline(mindlog);
}

describe("run block extent", () => {
  it("keeps a point block when the run has no end and no members", () => {
    const header = step("2026-09-03T12:00:00Z", { type: "shellm-run" });
    const later = step("2026-09-03T12:10:00Z");
    const layout = build([header, later], [run("r1", header, [])]);
    const block = layout.blocks[0];
    expect(block).toBeDefined();
    expect(block.endRow).toBe(block.startRow);
  });

  it("spans a closed run down to the last row at/before its end", () => {
    const t0 = "2026-09-03T12:00:00Z";
    const t1 = "2026-09-03T12:01:00Z";
    const t2 = "2026-09-03T12:02:00Z";
    const t3 = "2026-09-03T12:03:00Z";
    const t4 = "2026-09-03T12:04:00Z";
    const header = step(t0, { type: "shellm-run" });
    const inside = step(t2);
    const boundary = step(t3);
    const after = step(t4);
    const layout = build(
      [header, step(t1), inside, boundary, after],
      // run ends at t3: its block reaches the t3 row; t4 is outside
      [run("r1", header, [], { ended_ts: t3, status: "done" })]
    );
    const block = layout.blocks[0];
    const insideRow = layout.cells.find((c) => c.step === inside)!.row;
    const boundaryRow = layout.cells.find((c) => c.step === boundary)!.row;
    const afterRow = layout.cells.find((c) => c.step === after)!.row;
    expect(block.endRow).toBeGreaterThan(insideRow);
    expect(block.endRow).toBe(boundaryRow);
    expect(block.endRow).toBeLessThan(afterRow);
  });

  it("uses the latest member timestamp for an open run", () => {
    const t0 = "2026-09-03T12:00:00Z";
    const t1 = "2026-09-03T12:01:00Z";
    const t2 = "2026-09-03T12:02:00Z";
    const t3 = "2026-09-03T12:03:00Z";
    const header = step(t0, { type: "shellm-run" });
    const boundary = step(t1);
    // Run members render inside the block (no row of their own) but their
    // timestamps still drive an open run's extent.
    const member = step(t2);
    const after = step(t3);
    const layout = build(
      [header, boundary, member, after],
      [run("r1", header, [member])]
    );
    const block = layout.blocks[0];
    const boundaryRow = layout.cells.find((c) => c.step === boundary)!.row;
    const afterRow = layout.cells.find((c) => c.step === after)!.row;
    expect(block.endRow).toBe(boundaryRow);
    expect(block.endRow).toBeLessThan(afterRow);
  });

  it("skips gap-divider rows (rowMs = 0) without ending the block early", () => {
    const t0 = "2026-09-03T12:00:00Z";
    const t1 = "2026-09-03T12:01:00Z";
    const t2 = "2026-09-03T12:20:00Z"; // > 1 min gap → divider row before it
    const t2b = "2026-09-03T12:21:00Z";
    const t3 = "2026-09-03T12:40:00Z";
    const header = step(t0, { type: "shellm-run" });
    const inside = step(t2b);
    const after = step(t3);
    const layout = build(
      [header, step(t1), step(t2), inside, after],
      // run ends at t2b — the divider row (undated) sits before it
      [run("r1", header, [], { ended_ts: t2b, status: "done" })]
    );
    const block = layout.blocks[0];
    const insideRow = layout.cells.find((c) => c.step === inside)!.row;
    const afterRow = layout.cells.find((c) => c.step === after)!.row;
    expect(block.endRow).toBe(insideRow);
    expect(block.endRow).toBeLessThan(afterRow);
  });
});

describe("layout geometry", () => {
  it("places each row below the previous one and centers cells within it", () => {
    const steps = [
      step("2026-09-03T12:00:00Z"),
      step("2026-09-03T12:01:00Z", { source: "actor" }),
      step("2026-09-03T12:02:00Z", { source: "inner_monologue" }),
    ];
    const layout = build(steps, []);
    expect(layout.rowY.length).toBe(layout.rowH.length);
    let y = 0;
    for (let r = 0; r < layout.rowH.length; r++) {
      expect(layout.rowY[r]).toBe(y);
      expect(rowCenterY(layout, r)).toBe(y + layout.rowH[r] / 2);
      y += layout.rowH[r];
    }
    expect(layout.totalHeight).toBe(y);
  });

  it("collapses idle chains into their head cell with count and span", () => {
    const base = "2026-09-03T12:00:00Z";
    const idles = [
      step(base, { type: "idle" }),
      step("2026-09-03T12:01:00Z", { type: "idle" }),
      step("2026-09-03T12:02:00Z", { type: "idle" }),
    ];
    const wake = step("2026-09-03T12:03:00Z");
    const layout = build([...idles, wake], []);
    expect(layout.cells.length).toBe(2); // 1 idle head + the wake
    const idleCell = layout.cells[0];
    expect(idleCell.idleCount).toBe(3);
    expect(idleCell.idleSpan).toBe("2m 0s");
  });
});
