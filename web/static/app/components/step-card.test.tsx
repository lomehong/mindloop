import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StepContent } from "~/components/step-card";
import type { NormalizedStep } from "~/lib/types";

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

describe("Slack source links", () => {
  it("uses the exact inherited permalink on a reply step", () => {
    const sourceUrl =
      "https://laudesters.slack.com/archives/C123/p1788451200123456";
    const step: NormalizedStep = {
      step_id: "m2",
      ts: "2026-09-03T12:00:01Z",
      type: "message",
      source: "chat",
      preview: "done",
      raw: {
        from: "audel",
        to: "slack-U1-C123-1788451200.123456",
        content: "done",
        reply_to: "m1",
      },
      run_id: null,
      source_url: sourceUrl,
    };

    render(<StepContent step={step} expandAll />);

    const link = screen.getByRole("link", { name: /open in slack/i });
    expect(link.getAttribute("href")).toBe(sourceUrl);
  });
});
