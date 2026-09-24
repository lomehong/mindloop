// Deeplink behavior of the Timeline tab: ?step=/?run= resolution at mount
// (including a first layout that hasn't loaded yet), reconciliation with
// later URL changes (soft navigation, Back/Forward), fallbacks for targets
// outside the loaded window, follow-mode pinning, the URL round-trip
// of opening/closing the detail modal, and the substep copy-link URL.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TimelineView } from "~/components/timeline-view";
import { buildTimeline, type TimelineLayout } from "~/lib/timeline-model";
import { TrajContext, type TrajContextValue } from "~/lib/traj-context";
import type { NormalizedStep, RunGroup, StepSource, StepType } from "~/lib/types";

// jsdom ships no ResizeObserver; ExpandableText measures with one.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

// The fetch-one fallback (StepModal) and run-command hydration hit the
// network; fail the former so the fallback's terminal state is stable.
vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchStep: vi.fn(async () => {
      throw new Error("step not on server");
    }),
    fetchRunCommand: vi.fn(async () => ({ command: "echo hi" })),
  };
});

// --- fixture: one thought cell + one finished run with machinery members ---

function mkStep(over: {
  step_id: string;
  ts: string;
  type: StepType;
  source?: StepSource;
  preview?: string;
  raw?: Record<string, unknown>;
}): NormalizedStep {
  return {
    step_id: over.step_id,
    ts: over.ts,
    type: over.type,
    source: over.source ?? null,
    preview: over.preview ?? "",
    raw: over.raw ?? {},
    run_id: null,
  };
}

const STEPS: NormalizedStep[] = [
  mkStep({
    step_id: "s-thought",
    ts: "2026-08-29T10:00:00Z",
    type: "thought",
    source: "inner_monologue",
    preview: "pondering the timeline",
    raw: { content: "full pondering content" },
  }),
  mkStep({
    step_id: "s-run-header",
    ts: "2026-08-29T10:00:10Z",
    type: "shellm-run",
    preview: "run header",
    raw: { run_id: "r1" },
  }),
  mkStep({
    step_id: "s-final",
    ts: "2026-08-29T10:00:20Z",
    type: "final",
    preview: "final words",
    raw: { run_id: "r1", content: "final machinery content" },
  }),
];

const RUNS: RunGroup[] = [
  {
    run_id: "r1",
    trigger_step_id: null,
    launched_by: "actor",
    step_ids: ["s-run-header", "s-final"],
    started_ts: "2026-08-29T10:00:10Z",
    ended_ts: "2026-08-29T10:00:30Z",
    status: "done",
    command: "echo hi",
    model: null,
    tldr: "did a thing",
    last_touch: 0,
  },
];

const fullLayout = () => buildTimeline({ steps: STEPS, runs: RUNS });
const emptyLayout = () => buildTimeline({ steps: [], runs: [] });

const TRAJ: TrajContextValue = { identityId: "ada", trajId: "t1" };

// --- harness ---------------------------------------------------------------

interface Opts {
  url?: string;
  layout?: TimelineLayout;
  live?: boolean;
  traj?: TrajContextValue | null;
}

function renderTimeline(opts: Opts = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const updates: { searchParams: URLSearchParams; queryString: string }[] = [];
  const ui = (o: Opts) => (
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <TrajContext.Provider value={o.traj === undefined ? TRAJ : o.traj}>
          <NuqsTestingAdapter
            searchParams={o.url ?? ""}
            hasMemory
            onUrlUpdate={(e) => updates.push(e)}
          >
            <TimelineView layout={o.layout ?? fullLayout()} live={o.live ?? false} />
          </NuqsTestingAdapter>
        </TrajContext.Provider>
      </QueryClientProvider>
    </MemoryRouter>
  );
  const view = render(ui(opts));
  return {
    ...view,
    updates,
    // Simulates a soft navigation / Back-Forward: the adapter (hasMemory)
    // syncs its params to the new searchParams prop, like a URL change
    // arriving from outside the component.
    update: (next: Opts) => {
      opts = { ...opts, ...next };
      view.rerender(ui(opts));
    },
  };
}

// jsdom never scrolls, so record scrollTop writes to observe scroll-to-row.
let scrollTopWrites: number[] = [];
const origScrollTop = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  "scrollTop"
);
beforeEach(() => {
  scrollTopWrites = [];
  Object.defineProperty(HTMLElement.prototype, "scrollTop", {
    configurable: true,
    get() {
      return 0;
    },
    set(v: number) {
      scrollTopWrites.push(v);
    },
  });
});
afterEach(() => {
  if (origScrollTop) {
    Object.defineProperty(HTMLElement.prototype, "scrollTop", origScrollTop);
  } else {
    delete (HTMLElement.prototype as unknown as Record<string, unknown>)
      .scrollTop;
  }
  cleanup();
});

// --- tests -----------------------------------------------------------------

describe("timeline deeplinks", () => {
  it("?step= for an in-window step opens its modal, scrolls, and flashes the cell", async () => {
    const { container } = renderTimeline({ url: "?step=s-thought" });
    await screen.findByText("full pondering content");
    const flash = container.querySelector(".tl-flash");
    expect(flash).not.toBeNull();
    // the flash sits on the cell's square, not on a run block
    expect(flash!.getAttribute("title")).toBeNull();
    expect(scrollTopWrites.length).toBeGreaterThan(0);
  });

  it("?step= for a machinery step with no cell lands on its run block", async () => {
    const { container } = renderTimeline({ url: "?step=s-final" });
    await screen.findByText("final machinery content");
    const flash = container.querySelector(".tl-flash");
    expect(flash).not.toBeNull();
    expect(flash!.getAttribute("title")).toContain("[run]");
  });

  it("?run= for a loaded run opens the run modal and flashes its block", async () => {
    const { container } = renderTimeline({ url: "?run=r1" });
    await screen.findByText("steps (2)");
    const flash = container.querySelector(".tl-flash");
    expect(flash).not.toBeNull();
    expect(flash!.getAttribute("title")).toContain("[run]");
  });

  it("an unknown step id falls back to the fetch-one StepModal", async () => {
    renderTimeline({ url: "?step=nope" });
    await screen.findByText("Step not found.");
  });

  it("an unknown run id shows the load-older notice", async () => {
    renderTimeline({ url: "?run=ghost" });
    await screen.findByText(/older than the loaded part of the timeline/);
  });

  it("an unknown step id without traj context still shows a notice", async () => {
    renderTimeline({ url: "?step=nope", traj: null });
    await screen.findByText(/not in the loaded part of the timeline/);
  });

  it("holds a pending deeplink while the layout is still empty", async () => {
    // Pre-fix, an empty first layout consumed the deeplink and declared the
    // step missing; the real modal never opened once data arrived.
    const view = renderTimeline({ url: "?step=s-thought", layout: emptyLayout() });
    expect(screen.queryByText("loading…")).toBeNull();
    expect(screen.queryByText("Step not found.")).toBeNull();
    view.update({ layout: fullLayout() });
    await screen.findByText("full pondering content");
  });

  it("follows later URL changes and closes when the params clear", async () => {
    const view = renderTimeline({ url: "?step=s-thought" });
    await screen.findByText("full pondering content");
    view.update({ url: "?run=r1" });
    await screen.findByText("steps (2)");
    expect(screen.queryByText("full pondering content")).toBeNull();
    view.update({ url: "" });
    await waitFor(() => expect(screen.queryByText("steps (2)")).toBeNull());
    view.update({ url: "?step=s-thought" });
    await screen.findByText("full pondering content");
  });

  it("normalizes a URL carrying both params down to the winner", async () => {
    const { updates } = renderTimeline({ url: "?step=s-thought&run=r1" });
    await screen.findByText("full pondering content");
    await waitFor(() => {
      const last = updates.at(-1);
      expect(last).toBeDefined();
      expect(last!.queryString).toBe("?step=s-thought");
    });
  });

  it("arrives unpinned with a deeplink param and pinned without", async () => {
    const withParam = renderTimeline({ url: "?step=s-thought", live: true });
    await screen.findByText("paused · resume");
    withParam.unmount();
    renderTimeline({ url: "", live: true });
    await screen.findByText("following");
  });

  it("a rejecting clipboard leaves no unhandled rejection behind copy-link", async () => {
    // vitest fails the test on any unhandled rejection, so surviving the
    // flushed microtask below is the assertion.
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: vi.fn(async () => {
          throw new DOMException("denied", "NotAllowedError");
        }),
      },
    });
    try {
      renderTimeline({ url: "?run=r1" });
      await screen.findByText("steps (2)");
      fireEvent.click(screen.getByLabelText("Copy link"));
      await new Promise((r) => setTimeout(r, 0));
    } finally {
      delete (navigator as unknown as Record<string, unknown>).clipboard;
    }
  });

  it("a substep copy builds a ?step= deeplink, dropping ?run= and keeping other params", async () => {
    // SubstepLink builds from window.location.href, not the nuqs adapter, so
    // stage a real URL carrying the run param plus an unrelated one.
    window.history.replaceState(null, "", "/?run=r1&theme=dark");
    const written: string[] = [];
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn(async (t: string) => void written.push(t)) },
    });
    try {
      renderTimeline({ url: "?run=r1" });
      fireEvent.click(await screen.findByText("steps (2)"));
      const buttons = screen.getAllByLabelText("Copy link to this step");
      expect(buttons).toHaveLength(2);
      for (const b of buttons) fireEvent.click(b);
      await waitFor(() => expect(written).toHaveLength(2));
      const urls = written.map((w) => new URL(w));
      expect(urls.map((u) => u.searchParams.get("step")).sort()).toEqual([
        "s-final",
        "s-run-header",
      ]);
      for (const u of urls) {
        expect(u.searchParams.get("run")).toBeNull();
        expect(u.searchParams.get("theme")).toBe("dark");
      }
    } finally {
      delete (navigator as unknown as Record<string, unknown>).clipboard;
      window.history.replaceState(null, "", "/");
    }
  });

  it("opening writes the param and closing clears it", async () => {
    const { updates } = renderTimeline({ url: "" });
    fireEvent.click(screen.getByTitle("[thought] pondering the timeline"));
    await waitFor(() => {
      expect(updates.at(-1)?.searchParams.get("step")).toBe("s-thought");
    });
    fireEvent.click(screen.getByLabelText("Close"));
    await waitFor(() => {
      expect(updates.at(-1)?.searchParams.get("step")).toBeNull();
    });
  });
});
