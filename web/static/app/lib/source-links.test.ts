import { describe, expect, it } from "vitest";

import { slackConversationUrl, slackSourceUrl } from "~/lib/source-links";

describe("slackSourceUrl", () => {
  it("accepts Slack archive permalinks", () => {
    const url = "https://laudesters.slack.com/archives/C123/p1788451200123456";
    expect(slackSourceUrl(url)).toBe(url);
  });

  it("rejects unsafe and lookalike URLs", () => {
    expect(slackSourceUrl("javascript:alert(1)")).toBeNull();
    expect(
      slackSourceUrl("https://laudesters.slack.com.evil.example/archives/C123/p1")
    ).toBeNull();
    expect(slackSourceUrl("https://laudesters.slack.com/not-archives/C123")).toBeNull();
  });
});

describe("slackConversationUrl", () => {
  it("builds a fallback link from old Slack routing names", () => {
    expect(slackConversationUrl("slack-U123-C456-1788451200.123456")).toBe(
      "https://slack.com/app_redirect?channel=C456"
    );
    expect(slackConversationUrl("slack-U123-D456")).toBe(
      "https://slack.com/app_redirect?channel=D456"
    );
  });

  it("ignores non-Slack and malformed names", () => {
    expect(slackConversationUrl("pwa-nick")).toBeNull();
    expect(slackConversationUrl("slack-U123-C456-extra")).toBeNull();
  });
});
