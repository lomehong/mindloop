// Wire types for the shellm web viewer API.

export interface Config {
  root: string;
  version: string;
  controls_enabled: boolean;
  self_update_enabled: boolean;
  default_send_from: string | null;
  git_commit: string | null;
  git_branch: string | null;
}

export interface LlmHealthIdentity {
  id: string;
  name: string;
  live: boolean;
  failures_1h: number;
  failures_15m: number;
  last_failure: { ts: string; content: string } | null;
  cadence: {
    recent_median_s: number;
    baseline_median_s: number | null;
    recent_n: number;
  } | null;
}

export interface LlmLastCall {
  ok: boolean;
  ts: string | null;
  provider: string | null;
  model: string | null;
  kind: "credit" | "auth" | "rate" | "other" | null;
  http_code: number | string | null;
  message: string | null;
}

export interface LlmHealth {
  status: "ok" | "degraded" | "erroring" | "unknown";
  failures_15m: number;
  failures_1h: number;
  cadence_slow: boolean;
  checked_at: string;
  identities: LlmHealthIdentity[];
  last_call?: LlmLastCall | null;
}

export interface LlmProbeResult {
  ok: boolean;
  latency_ms: number;
  model: string | null;
  provider?: string | null;
  error?: string;
}

export interface SelfUpdateResult {
  ok: boolean;
  updated: boolean;
  restarting: boolean;
  commit?: string;
  from_commit?: string;
  to_commit?: string;
}

export interface DispatcherStatus {
  running: boolean;
  pid: number | null;
}

export interface Identity {
  id: string;
  name: string;
  path_rel: string;
  created: string | null;
  root_trajectory: string | null;
  group: string;
  live: boolean;
  last_activity_ts: string | null;
  step_count: number;
  dispatcher: DispatcherStatus;
  thinkers_total: number;
  thinkers_active: number;
}

export type ThinkerState =
  | "stopped"
  | "idle"
  | "active"
  | "running"
  | "draining"
  | "disabled";

export interface ThinkerInfo {
  name: string;
  state: ThinkerState;
  pid: number | null;
  types: string[];
  trigger_self: boolean;
  log_bytes: number | null;
  log_mtime: string | null;
}

export interface ThinkersStatus {
  identity: { id: string; name: string };
  dispatcher: DispatcherStatus;
  active_thinkers: number;
  thinkers_total: number;
  thinkers_disabled: number;
  thinkers: ThinkerInfo[];
}

export interface ControlResult {
  ok: boolean;
  action: string;
  names: string[];
  exit_code?: number;
  stdout?: string;
  stderr?: string;
}

export interface ChatMessage {
  ts: string | null;
  step_id: string | null;
  from: string;
  to: string;
  content: string;
  reply_to: string | null;
  filename: string | null;
  source_url: string | null;
}

export interface ChatLog {
  identity: { id: string; name: string };
  live: boolean;
  messages: ChatMessage[];
  // sent step_id -> "replied" | "no-reply" | "failed"; absent = undecided
  outcomes: Record<string, string>;
}

export interface EnvEntry {
  key: string;
  value: string; // full value for non-secrets, redacted peek for secrets
  secret: boolean;
  overridden?: boolean; // inherited entries only
}

export type ThinkerSyncState =
  | "in_sync"
  | "outdated"
  | "not_installed"
  | "local_only";

export interface ThinkerSyncEntry {
  name: string;
  status: ThinkerSyncState;
  changed_files: string[];
  bundled_version: string | null; // "shorthash · date" of the bundled copy
}

export interface ThinkerSyncStatus {
  bundled_root: string | null;
  thinkers: ThinkerSyncEntry[];
  note: string;
}

export interface ThinkerSyncResult {
  ok: boolean;
  results: { name: string; action: string; files: string[] }[];
}

export interface OpenRouterModel {
  id: string;
  name: string | null;
  context_length: number | null;
  prompt_usd_per_m: number | null;
  completion_usd_per_m: number | null;
}

export interface OpenRouterModels {
  source: "key" | "public" | null; // "key": filtered to this org's key
  has_key: boolean;
  count: number;
  models: OpenRouterModel[];
  error: string | null;
  fetched_at: string;
}

export interface IdentityEnv {
  identity: { id: string; name: string };
  env: EnvEntry[];
  inherited: EnvEntry[];
  note: string;
}

/** providers.json 有效视图（两级合并）里的单个提供商档案——
 * 连接与模型清单非敏感；密钥只以键名与命中状态出场。 */
export interface LlmProviderProfileView {
  id: string;
  label: string;
  provider: string; // "" = 按模型名推断 | "anthropic" | "openai-compatible" | "echo"
  base_url: string;
  api_key_env: string; // 显式引用的 .env 键名；空 = 用约定键
  models: string[];
  key_env_name: string; // 实际查找的密钥键名（显式或约定）
  has_key: boolean;
  key_source: "" | "env" | "identity";
  /** 档案归属层级：identity=身份级存在（含覆盖全局），
   * global=仅全局级——后者删除会回潮，配置页据此禁用删除。 */
  origin: "identity" | "global";
}

/** providers.json 的档位绑定（think/request/summary）。 */
export interface LlmTierBinding {
  profile: string;
  model: string;
}

/** GET/PUT /llm/providers 的响应形态。 */
export interface LlmProvidersView {
  identity: { id: string; name: string };
  version: number;
  profiles: LlmProviderProfileView[];
  /** 两级合并后的有效档位（读面：显示用）。 */
  tiers: Record<string, LlmTierBinding>;
  /** 身份文档的原始档位（写面基线）：PUT 时以此为底做最小
   * 写入——合并视图里的全局档位不回写进身份文档。 */
  identity_tiers: Record<string, LlmTierBinding>;
  paths: { global: string; identity: string };
  error?: string;
}

/** PUT /llm/providers 的档案输入——api_key 仅请求体存在，
 * 服务端分流写入身份 .env，绝不落 providers.json。 */
export interface LlmProviderProfileInput {
  id: string;
  label?: string;
  provider?: string;
  base_url: string;
  api_key_env?: string;
  models?: string[];
  api_key?: string;
}

/** PUT /llm/providers 的请求体。 */
export interface LlmProvidersSaveInput {
  version?: number;
  profiles: LlmProviderProfileInput[];
  tiers: Record<string, LlmTierBinding>;
}

/** GET /llm/models 的探测结果（错误装 error 字段，非错误码）。 */
export interface LlmModelsProbe {
  profile: string;
  models: string[];
  source: "live" | "cache" | "";
  error?: string;
}

/** 单个档位的有效解析（与 CLI 同一解析器）。 */
export interface LlmTierResolved {
  source: "" | "env" | "providers.json";
  model: string;
  profile: string;
  error?: string;
}

/** GET /llm/config：think/request/summary 三档位解析结果。 */
export interface LlmConfigView {
  tiers: Record<"think" | "request" | "summary", LlmTierResolved>;
  error?: string;
}

export interface KillallResult {
  ok: boolean;
  dry_run: boolean;
  stdout: string;
  stderr: string;
}

export interface ImportResult {
  ok: boolean;
  imported: { id: string; name: string }[];
}

export interface RecapStepRef {
  step: string;
  note: string;
}

export interface RecapTheme {
  name: string;
  description: string;
  episodes: number[];
  key_steps: RecapStepRef[];
}

export interface RecapEpisode {
  idx: number;
  first_step: string;
  last_step: string;
  first_ts: string;
  last_ts: string;
  n_steps: number;
  partial: boolean;
  title: string;
  summary: string;
  themes: string[];
  notable_steps: RecapStepRef[];
}

export interface Recap {
  identity: { id: string; name: string };
  available: boolean;
  refreshing: boolean;
  new_steps?: number;
  themes?: {
    generated_at: string;
    model: string;
    arc: string;
    themes: RecapTheme[];
  };
  episodes?: RecapEpisode[];
}

/** One UTC day of the usage series (see headlong_web/usage.py). */
export interface UsageDay {
  rows: number;
  in_msg: number;
  out_msg: number;
  runs: number;
  reasoning: number;
  calls: number;
  /** Successful calls whose provider returned no usage (usage_known=false).
   * Explicitly counted — never presented as zero-cost. */
  unknown: number;
  in: number;
  out: number;
  think: number;
  /** Where calls/tokens came from: the bin/llm ledger (every call) or the
   * mind log's reasoning-step stamps (shellm runs only, older days). */
  source: "ledger" | "mindlog";
}

export interface UsageModel {
  calls: number;
  in: number;
  out: number;
  think: number;
  unknown_calls?: number;
}

/** Daily token budget and circuit-breaker state (obs.AdmissionStatus).
 * daily_limit=0 means the budget is not configured. */
export interface UsageAdmission {
  daily_limit: number;
  used_today: number;
  consecutive_errors: number;
  circuit_threshold: number;
  /** RFC3339 deadline while the circuit is cooling; absent = not cooling. */
  cooling_until?: string;
}

export interface Usage {
  identity: { id: string; name: string };
  available: boolean;
  refreshing: boolean;
  /** Bytes appended to the mind log since the cache was computed. */
  pending_bytes: number;
  generated?: string;
  rows?: number;
  skipped?: number;
  /** The bin/llm usage ledger: lines read, lines without usable usage, and
   * the first day it covers (null when it has no calls yet). */
  ledger?: { rows: number; skipped: number; since: string | null };
  daily?: [string, UsageDay][];
  by_model?: Record<string, UsageModel>;
  totals?: {
    in: number;
    out: number;
    think: number;
    calls: number;
    in_msg: number;
    out_msg: number;
    runs: number;
    unknown_calls?: number;
  };
  /** Budget/circuit snapshot; absent on old data — page must not break. */
  admission?: UsageAdmission;
}

export interface IdentityStatus {
  live: boolean;
  pid_alive: boolean;
  dispatcher_pid: number | null;
  mindlog_mtime: string | null;
  mindlog_bytes: number | null;
  step_count: number;
}

export type ActivityState = "working" | "stalled" | "idle" | "asleep";

export interface IdentityActivity {
  state: ActivityState;
  dispatcher_running: boolean;
  busy_thinkers: string[];
  last_step_ts: string | null;
  last_step_age_s: number | null;
  run_seconds: number | null;
  stall_after_s: number;
  cadence_s: number | null;
}

export interface ResponseEvent {
  ts: string | null;
  from: string;
  outcome: "replied" | "declined";
  path: "inline" | "fast" | null;
  response_s: number;
}

export interface PathStats {
  n: number;
  median_s: number | null;
  p90_s: number | null;
}

export interface InjectionEvent {
  ts: string | null;
  from: string;
  inject_ms: number;
  wait_s: number | null;
  model_s: number | null;
  total_s: number | null;
  path: "inline" | "fast" | null;
}

export interface ModelDaily {
  day: string;
  calls: number;
  in_tok: number;
  out_tok: number;
  think_tok: number;
}

export interface ModelStats {
  calls: number;
  llm_p50_s: number | null;
  llm_p90_s: number | null;
  in_tok: number;
  out_tok: number;
  think_tok: number;
  daily: ModelDaily[];
}

export interface ResponseStats {
  window_days: number;
  replied: number;
  declined: number;
  undecided: number;
  duplicates: number;
  median_s: number | null;
  p90_s: number | null;
  max_s: number | null;
  paths: { fast: PathStats; inline: PathStats };
  injections: InjectionEvent[];
  model: ModelStats;
  recent: ResponseEvent[];
}

export interface IdentityHealth {
  identity: { id: string; name: string };
  activity: IdentityActivity;
  responses: ResponseStats | null;
}

export type StepType =
  | "trajectory"
  | "thought"
  | "action"
  | "idle"
  | "observation"
  | "message"
  | "shellm-run"
  | "prompt"
  | "reasoning"
  | "shell-output"
  | "feedback"
  | "final"
  | "fork"
  | "run-summary"
  | "tp-thought"
  | "human-msg"
  | "agent-msg"
  | "merge";

export type StepSource =
  | "seed"
  | "inner_monologue"
  | "actor"
  | "chat"
  | (string & {})
  | null;

export interface ForkLink {
  child_traj_id: string;
  slug: string;
  resolved: boolean;
}

export interface WritebackLink {
  from_traj: string;
  from_step: string | null;
}

export interface NormalizedStep {
  step_id: string;
  ts: string;
  type: StepType;
  source: StepSource;
  preview: string;
  raw: Record<string, unknown>;
  run_id: string | null;
  source_url?: string | null;
  fork?: ForkLink;
  writeback?: WritebackLink;
}

export interface RunGroup {
  run_id: string;
  trigger_step_id: string | null;
  launched_by: string | null;
  step_ids: string[];
  started_ts: string;
  ended_ts: string | null;
  status: "running" | "done";
  /** Truncated on the wire when huge (head + trailing ACTION kept);
   * fetch the full text via fetchRunCommand when command_truncated. */
  command: string;
  command_truncated?: boolean;
  model: string | null;
  tldr: string | null;
  /** Step index of the last step that mutated this run (delta filtering). */
  last_touch: number;
}

export interface Mindlog {
  traj_id: string;
  dir_rel: string;
  step_count: number;
  /** The requested window (initial ?tail, ?since polls, or ?since+?until
   * history loads); the useMindlog hook stitches windows together. */
  steps: NormalizedStep[];
  runs: RunGroup[];
  live: boolean;
  /** Effective start index of `steps` in the full log (null = 0). */
  since?: number | null;
  identity: { id: string; name: string };
}

export interface SearchHit {
  /** Absolute step index in the full log — >= the loaded window's start
   * means the step is on the page and can be scrolled to. */
  index: number;
  step_id: string;
  ts: string | null;
  type: string;
  source: string | null;
  run_id: string | null;
  /** Which field matched (content, thought, cmd, stdout, …). */
  field: string;
  snippet: string;
}

export interface MindlogSearchResult {
  q: string;
  scope: "thoughts" | "all";
  total: number;
  hits: SearchHit[];
  step_count: number;
  identity: { id: string; name: string };
}

export interface StepDetail {
  step: NormalizedStep;
  index: number;
  run: RunGroup | null;
}

export interface TreeNode {
  traj_id: string;
  slug: string;
  parent_step_id: string | null;
  started_ts: string;
  last_ts: string;
  step_count: number;
  has_final: boolean;
  tldr: string | null;
  child_count: number;
  children?: TreeNode[];
}

export interface Crumb {
  traj_id: string;
  slug: string;
}

export interface SubTrajectory extends Mindlog {
  breadcrumb: Crumb[];
  parent: { traj_id: string; step_id: string | null } | null;
}

export interface LogInfo {
  name: string;
  bytes: number;
  mtime: number;
}

export interface LogTail {
  name: string;
  content: string;
  total_bytes: number;
  truncated: boolean;
}

export interface DispatchEvent {
  kind: "step" | "dispatch" | "other";
  type?: string;
  source?: string | null;
  thinker?: string;
  active?: number | null;
  raw: string;
}

export interface SkillEntry {
  name: string;
  description: string;
  source: "identity" | "global";
  dir: string;
}

export interface SkillsView {
  identity: { id: string; name: string };
  skills: SkillEntry[];
  problems: string[];
}

export interface MemoryInfo {
  name: string;
  mtime: number;
  id: string | null;
  summary: string | null;
  type: string;
  created: string | null;
  slug: string;
  /** 空串或 "active" = 活动；invalid/superseded 已退出检索与显式操作。 */
  status?: string;
}

// ---- 任务（显式委托）：字段口径与 internal/task/task.go 的 JSON 对齐 ----

export type TaskState =
  | "queued"
  | "running"
  | "awaiting_approval"
  | "canceling"
  | "succeeded"
  | "failed"
  | "canceled"
  | "interrupted"
  | "budget_exceeded";

/** 状态变化及控制命令的收据（追加式事实，旧 attempt 的证据不被覆盖）。 */
export interface TaskEvent {
  step_id: string;
  ts: string;
  task_id: string;
  status: TaskState;
  attempt: number;
  run_id?: string;
  reason?: string;
  result?: string;
  result_kind?: string;
  evidence_step_ids?: string[];
  operation: string;
  actor?: string;
  request_id?: string;
  base_attempt?: number;
}

/** 从根轨迹投影出的任务视图；status/result 是事实的最终呈现。 */
export interface AgentTask {
  task_id: string;
  identity_id: string;
  from: string;
  client_message_id: string;
  content: string;
  source_step_id?: string;
  status: TaskState;
  attempt: number;
  run_id?: string;
  created_at: string;
  updated_at: string;
  reason?: string;
  result?: string;
  result_kind?: string;
  evidence_step_ids?: string[];
  events: TaskEvent[];
}

// ---- 执行授权（等待批准的脚本）：字段口径与 internal/policy/control.go
// 的 PendingRequest JSON 对齐 ----

/** 等待批准的脚本——审批控制面的展示载荷（正文完整呈现："执行前
 * 展示正文"是 ask 策略的定义）。 */
export interface PendingApproval {
  hash: string;
  script: string;
  work_dir: string;
  run_id: string;
  task_id?: string;
  attempt?: number;
  created: string;
  expires: string;
  risks?: string[];
}
