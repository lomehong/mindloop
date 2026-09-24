// Presentation mapping for the Mind log v2 tab: which steps show, and how.
//
// The v2 tab is a curated live stream: only thoughts, observations, and
// messages render, everything else (runs, prompts, actions, errors) is
// dropped. This mapping is deliberately separate from step-card.tsx — the
// operator mind log and the presentation stream want opposite things.

import type { NormalizedStep } from "~/lib/types";
import { slackConversationUrl, slackSourceUrl } from "~/lib/source-links";

export type CardKind = "thought" | "observation" | "outbound" | "inbound";

/** The legend groups (and filter toggles): message covers both directions. */
export type CardGroup = "thought" | "observation" | "message";

export interface Ml2Card {
  kind: CardKind;
  group: CardGroup;
  /** Small mono heading, e.g. "inner monologue" or "sent to Nick via Telegram". */
  label: string;
  body: string;
  ts: string;
  step_id: string;
  source_url: string | null;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function capitalize(name: string): string {
  return name ? name.charAt(0).toUpperCase() + name.slice(1) : name;
}

/** Best-effort person + channel from a chat `from`/`to` name. Bridge names
 * are routing-encoded (slack-U07AB…-C09…, telegram-8631…), so the person is
 * often unrecoverable client-side; we then name only the channel. */
function party(name: string): { person: string | null; channel: string | null } {
  if (!name) return { person: null, channel: null };
  const dash = name.indexOf("-");
  const prefix = dash > 0 ? name.slice(0, dash) : name;
  const rest = dash > 0 ? name.slice(dash + 1) : "";
  const restIsName = /^[a-z]+$/i.test(rest);
  switch (prefix) {
    case "slack":
      return { person: null, channel: "Slack" };
    case "telegram":
      return { person: restIsName ? capitalize(rest) : null, channel: "Telegram" };
    case "pwa":
      return { person: restIsName ? capitalize(rest) : null, channel: "chat" };
    default:
      // Plain names come from the dash chat (e.g. "operator", "nick").
      return { person: capitalize(name), channel: null };
  }
}

function messageLabel(direction: "outbound" | "inbound", other: string): string {
  const { person, channel } = party(other);
  if (direction === "outbound") {
    if (person && channel) return `sent to ${person} via ${channel}`;
    if (channel) return `sent via ${channel}`;
    if (person) return `sent to ${person}`;
    return "message sent";
  }
  if (person && channel) return `${person} replied via ${channel}`;
  if (channel) return `reply via ${channel}`;
  if (person) return `${person} replied`;
  return "message received";
}

/** Map a step to a presentation card, or null to hide it. */
export function toCard(
  step: NormalizedStep,
  identityName: string
): Ml2Card | null {
  const raw = step.raw;
  const sourceUrl =
    slackSourceUrl(step.source_url) || slackSourceUrl(raw.source_url);
  const base = { ts: step.ts, step_id: step.step_id, source_url: sourceUrl };
  switch (step.type) {
    case "thought":
    case "tp-thought": {
      const body = str(raw.content) || str(raw.thought) || step.preview;
      if (!body) return null;
      return { ...base, kind: "thought", group: "thought", label: "inner monologue", body };
    }
    case "observation": {
      const body = str(raw.content) || step.preview;
      if (!body) return null;
      return { ...base, kind: "observation", group: "observation", label: "observation", body };
    }
    case "message":
    case "human-msg":
    case "agent-msg": {
      const body = str(raw.content) || step.preview;
      if (!body) return null;
      const from = str(raw.from);
      const to = str(raw.to);
      const outbound =
        step.type === "agent-msg" || (from !== "" && from === identityName);
      const direction = outbound ? ("outbound" as const) : ("inbound" as const);
      const other = outbound ? to : from;
      return {
        ...base,
        source_url: base.source_url || slackConversationUrl(other),
        kind: direction,
        group: "message",
        label: messageLabel(direction, other),
        body,
      };
    }
    default:
      return null;
  }
}
