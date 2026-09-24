import { describe, expect, it } from "vitest";

import { toCard } from "~/lib/mindlog2-model";
import type { NormalizedStep } from "~/lib/types";

function message(
  step_id: string,
  raw: Record<string, unknown>
): NormalizedStep {
  return {
    step_id,
    ts: "2026-09-03T12:00:00Z",
    type: "message",
    source: "chat",
    preview: String(raw.content ?? ""),
    raw,
    run_id: null,
  };
}

describe("mind log Slack links", () => {
  it("keeps the permalink on an inbound Slack card", () => {
    const url = "https://laudesters.slack.com/archives/C123/p1788451200123456";
    const card = toCard(
      message("m1", {
        from: "slack-U1-C123-1788451200.123456",
        to: "audel",
        content: "please check this",
        source_url: url,
      }),
      "audel"
    );
    expect(card?.source_url).toBe(url);
  });

  it("links a reply to the Slack message it answers", () => {
    const url = "https://laudesters.slack.com/archives/C123/p1788451200123456";
    const card = toCard(
      {
        ...message("m2", {
          from: "audel",
          to: "slack-U1-C123-1788451200.123456",
          content: "done",
          reply_to: "m1",
        }),
        source_url: url,
      },
      "audel"
    );
    expect(card?.source_url).toBe(url);
  });

  it("opens the Slack conversation for an old message with no permalink", () => {
    const card = toCard(
      message("m0", {
        from: "slack-U1-D123",
        to: "audel",
        content: "an old message",
      }),
      "audel"
    );
    expect(card?.source_url).toBe("https://slack.com/app_redirect?channel=D123");
  });
});
