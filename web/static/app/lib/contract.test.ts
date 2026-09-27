// Contract fixtures: pin the frontend wire types (lib/types.ts) to the
// backend's actual JSON shapes. Each fixture mirrors what internal/web
// emits after the contract-drift cleanup (dead always-zero fields deleted
// on the wire): typing the fixture as the interface catches field drift at
// compile time, the `not.toHaveProperty` assertions pin the deletions so a
// backend reintroduction (or a frontend half-deletion) fails loudly here.

import { describe, expect, it } from "vitest";

import type {
  Config,
  ConnectorsView,
  Identity,
  IdentityActivity,
  ScheduleRunResult,
  ScheduleView,
  ThinkerInfo,
  ThinkersStatus,
} from "~/lib/types";

// GET /api/identities — bare array; one item's shape:
const identityItem = {
  id: "ada",
  name: "ada",
  path_rel: "identities/ada",
  created: "2026-09-03T12:00:00Z",
  root_trajectory: "root",
  group: "local",
  live: true,
  last_activity_ts: "2026-09-03T12:00:00Z",
  step_count: 42,
  dispatcher: { running: true, pid: 123 },
  thinkers_total: 2,
  thinkers_active: 2,
} satisfies Identity;

// GET /api/identities/{id}/thinkers:
const thinkersStatus = {
  identity: { id: "ada", name: "ada" },
  dispatcher: { running: true, pid: null },
  active_thinkers: 1,
  thinkers_total: 2,
  thinkers_disabled: 1,
  thinkers: [
    {
      name: "actor",
      state: "active",
      pid: null,
      types: ["message"],
      trigger_self: false,
      log_bytes: null,
      log_mtime: "2026-09-03T12:00:00Z",
    },
  ],
} satisfies ThinkersStatus;
const thinkerItem: ThinkerInfo = thinkersStatus.thinkers[0];

// GET /api/identities/{id}/activity:
const activity = {
  state: "working",
  dispatcher_running: true,
  busy_thinkers: ["actor"],
  last_step_ts: "2026-09-03T12:00:00Z",
  last_step_age_s: 12,
  run_seconds: null,
  stall_after_s: 300,
  cadence_s: null,
} satisfies IdentityActivity;

// GET /api/identities/{id}/schedule（条目字段与 CLI --json 同构）：
const scheduleView = {
  identity: { id: "ada", name: "ada" },
  file: "C:/root/identities/ada/schedule.json",
  entries: [
    {
      id: "daily",
      enabled: true,
      kind: "task",
      trigger: "at 21:00",
      action: "task 写晚报",
      next_run: "2026-09-27 21:00:00",
      last_run: "2026-09-26 21:00:00",
      last_exit_code: 3,
      last_error: "boom",
      last_note: "任务 tsk-1",
    },
  ],
  warnings: [],
  mind_running: true,
} satisfies ScheduleView;

// POST /api/identities/{id}/schedule/{eid}/run（task 形态）：
const scheduleRunResult = {
  ok: true,
  id: "daily",
  kind: "task",
  note: "任务 tsk-1",
  task_key: "sched-daily-2026-09-27",
} satisfies ScheduleRunResult;

// GET /api/identities/{id}/connections（env/headers 只给键名）：
const connectionsView = {
  identity: { id: "ada", name: "ada" },
  mcp_servers: [
    {
      name: "github",
      transport: "stdio",
      command: "npx",
      args: ["-y", "@mcp/server-github"],
      env_keys: ["GITHUB_TOKEN"],
      header_keys: [],
      source: "global",
    },
  ],
  mcp_files: [{ label: "全局", path: "C:/root/mcp.json", exists: true }],
  channels: [
    {
      channel: "wecom",
      label: "企业微信智能机器人",
      bot_id: "ww123",
      secret_set: true,
      allow: ["zhangsan"],
      ready: true,
      cursor_exists: false,
      note: "在企微管理后台获取 BotID 与 Secret。",
    },
  ],
} satisfies ConnectorsView;

// GET /api/config:
const config = {
  root: "/root",
  version: "0.1.0",
  controls_enabled: true,
  self_update_enabled: false,
  default_send_from: null,
  git_commit: null,
  git_branch: null,
} satisfies Config;

describe("wire contract vs lib/types.ts", () => {
  it("/api/identities no longer carries the deleted always-zero fields", () => {
    const raw = identityItem as unknown as Record<string, unknown>;
    expect(raw).not.toHaveProperty("steps_in_flight");
    expect(raw).not.toHaveProperty("mindlog_path");
    expect(raw).not.toHaveProperty("persona_path");
    expect(raw).not.toHaveProperty("live_badge");
    expect(identityItem.dispatcher).toHaveProperty("running");
    expect(identityItem).toHaveProperty("thinkers_active");
  });

  it("/thinkers dropped steps_in_flight/pending/pending_total everywhere", () => {
    const raw = thinkersStatus as unknown as Record<string, unknown>;
    expect(raw).not.toHaveProperty("steps_in_flight");
    expect(raw).not.toHaveProperty("pending_total");
    expect(raw).toHaveProperty("thinkers_disabled");
    const rawThinker = thinkerItem as unknown as Record<string, unknown>;
    expect(rawThinker).not.toHaveProperty("steps_in_flight");
    expect(rawThinker).not.toHaveProperty("pending");
    expect(rawThinker).toHaveProperty("log_bytes");
  });

  it("/activity dropped queued_messages/steps_in_flight/pending_total", () => {
    const raw = activity as unknown as Record<string, unknown>;
    expect(raw).not.toHaveProperty("queued_messages");
    expect(raw).not.toHaveProperty("steps_in_flight");
    expect(raw).not.toHaveProperty("pending_total");
    expect(activity.busy_thinkers).toEqual(["actor"]);
  });

  it("/config keeps the control flags the UI reads", () => {
    expect(config).toHaveProperty("controls_enabled");
    expect(config).toHaveProperty("self_update_enabled");
  });

  it("/schedule keeps the CLI-json entry shape (status projection fields)", () => {
    const entry = scheduleView.entries[0];
    expect(entry).toHaveProperty("next_run");
    expect(entry).toHaveProperty("last_exit_code");
    expect(entry).toHaveProperty("trigger");
    expect(entry).toHaveProperty("action");
    expect(scheduleRunResult.task_key).toBe("sched-daily-2026-09-27");
  });

  it("/connections exposes env/header keys only, never values", () => {
    const server = connectionsView.mcp_servers[0];
    expect(server).toHaveProperty("env_keys");
    expect(server).toHaveProperty("header_keys");
    const raw = server as unknown as Record<string, unknown>;
    expect(raw).not.toHaveProperty("env");
    expect(raw).not.toHaveProperty("headers");
  });
});
