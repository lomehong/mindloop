import type {
  AgentTask,
  ChatLog,
  Config,
  ControlResult,
  DispatchEvent,
  EnvEntry,
  Identity,
  IdentityActivity,
  IdentityEnv,
  IdentityHealth,
  ImportResult,
  LlmConfigView,
  LlmModelsProbe,
  LlmHealth,
  LlmProbeResult,
  LlmProvidersSaveInput,
  LlmProvidersView,
  IdentityStatus,
  KillallResult,
  LogInfo,
  LogTail,
  MemoryInfo,
  Mindlog,
  MindlogSearchResult,
  OpenRouterModels,
  PendingApproval,
  Recap,
  Usage,
  SelfUpdateResult,
  SkillsView,
  StepDetail,
  SubTrajectory,
  ThinkerSyncResult,
  ThinkerSyncStatus,
  ThinkersStatus,
  TreeNode,
} from "~/lib/types";

export const API_BASE = import.meta.env.VITE_API_URL ?? "";

// errorMessage 从错误响应里取人类可读消息——后端统一返回
// {detail: {message}}（中文），纯字符串 detail 也兼容。GET 与写
// 请求走同一解析，不再出现"读请求失败只显示英文状态码"。
async function errorMessage(response: Response): Promise<string> {
  let message = `${response.status} ${response.statusText}`;
  try {
    const data = await response.json();
    if (typeof data?.detail === "string") message = data.detail;
    else if (data?.detail?.message) message = data.detail.message;
  } catch {
    // keep default message
  }
  return message;
}

export class AuthError extends Error {
  constructor() {
    super("访问凭据缺失或已失效，请更新访问凭据。");
    this.name = "AuthError";
  }
}

async function apiRequest(url: string, init: RequestInit = {}): Promise<Response> {
  if (typeof window !== "undefined") {
    const target = new URL(url, window.location.href);
    const base = new URL(`${API_BASE}/api/`, window.location.href);
    if (target.origin !== base.origin || !target.pathname.startsWith(base.pathname) || target.username || target.password) {
      throw new Error("拒绝向 API 范围外的地址发送访问凭据");
    }
  }
  const token = webToken();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  else headers.delete("Authorization");
  const response = await fetch(url, { ...init, headers, redirect: "error" });
  if (response.status === 401) {
    if (token === webToken()) setAuthRequired(true);
    throw new AuthError();
  }
  if (!response.ok) throw new Error(await errorMessage(response));
  return response;
}

export async function fetchText(url: string): Promise<string> {
  return (await apiRequest(url)).text();
}

async function getJson<T>(path: string): Promise<T> {
  const response = await apiRequest(`${API_BASE}${path}`);
  return response.json() as Promise<T>;
}

async function sendJson<T>(
  method: string,
  path: string,
  body: unknown
): Promise<T> {
  const response = await apiRequest(`${API_BASE}${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return response.json() as Promise<T>;
}

function postJson<T>(path: string, body: unknown): Promise<T> {
  return sendJson("POST", path, body ?? {});
}

export function fetchConfig(): Promise<Config> {
  return getJson("/api/config");
}

export function selfUpdate(): Promise<SelfUpdateResult> {
  return postJson("/api/update", {});
}

export function fetchLlmHealth(): Promise<LlmHealth> {
  return getJson("/api/llm-health");
}

export function probeLlm(): Promise<LlmProbeResult> {
  return postJson("/api/llm-health/probe", {});
}

export function fetchIdentities(): Promise<Identity[]> {
  return getJson("/api/identities");
}

export function fetchIdentityStatus(identityId: string): Promise<IdentityStatus> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/status`);
}

export function fetchActivity(identityId: string): Promise<IdentityActivity> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/activity`);
}

export function fetchHealth(identityId: string): Promise<IdentityHealth> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/health`);
}

export function fetchMindlog(
  identityId: string,
  params: { since?: number; until?: number; tail?: number } = {}
): Promise<Mindlog> {
  const search = new URLSearchParams();
  if (params.since !== undefined) search.set("since", String(params.since));
  if (params.until !== undefined) search.set("until", String(params.until));
  if (params.tail !== undefined) search.set("tail", String(params.tail));
  const qs = search.toString();
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/mindlog${qs ? `?${qs}` : ""}`
  );
}

export function fetchRunCommand(
  identityId: string,
  runId: string
): Promise<{ run_id: string; command: string }> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/runs/${encodeURIComponent(runId)}/command`
  );
}

export function searchMindlog(
  identityId: string,
  q: string,
  scope: "thoughts" | "all",
  limit = 50
): Promise<MindlogSearchResult> {
  const params = new URLSearchParams({ q, scope, limit: String(limit) });
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/mindlog/search?${params}`
  );
}

export function fetchStep(
  identityId: string,
  stepId: string
): Promise<StepDetail> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/step/${encodeURIComponent(stepId)}`
  );
}

export function fetchTree(
  identityId: string,
  node?: string,
  depth = 2
): Promise<TreeNode> {
  const params = new URLSearchParams({ depth: String(depth) });
  if (node) params.set("node", node);
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/tree?${params}`
  );
}

export function fetchSubTraj(
  identityId: string,
  trajId: string
): Promise<SubTrajectory> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/traj/${encodeURIComponent(trajId)}`
  );
}

export function fetchLogs(identityId: string): Promise<LogInfo[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/logs`);
}

export function fetchLog(
  identityId: string,
  name: string,
  tailBytes = 65536
): Promise<LogTail> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/logs/${encodeURIComponent(name)}?tail_bytes=${tailBytes}`
  );
}

export function fetchDispatch(identityId: string): Promise<DispatchEvent[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/dispatch`);
}

export function fetchSkills(identityId: string): Promise<SkillsView> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/skills`
  );
}

export function installSkill(
  identityId: string,
  source: string
): Promise<{ ok: boolean; installed: string[] }> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/skills`,
    { source }
  );
}

export function removeSkill(
  identityId: string,
  name: string
): Promise<{ ok: boolean; removed: string }> {
  return sendJson(
    "DELETE",
    `/api/identities/${encodeURIComponent(identityId)}/skills/${encodeURIComponent(name)}`,
    undefined
  );
}

export function fetchMemories(identityId: string): Promise<MemoryInfo[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/memories`);
}

export function fetchMemory(
  identityId: string,
  name: string
): Promise<{ name: string; content: string }> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/memories/${encodeURIComponent(name)}`
  );
}

/** 修订一条记忆：后端写新版本、旧版本标记为被替代（文件保留）。 */
export function reviseMemory(
  identityId: string,
  id: string,
  content: string
): Promise<{ ok: boolean; id: string; supersedes: string }> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/memories/${encodeURIComponent(id)}/revise`,
    { content }
  );
}

/** 失效一条记忆：退出检索与引用，文件保留在盘上供审计。 */
export function invalidateMemory(
  identityId: string,
  id: string
): Promise<{ ok: boolean; id: string; status: string }> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/memories/${encodeURIComponent(id)}/invalidate`,
    undefined
  );
}

export function fetchRecap(identityId: string): Promise<Recap> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/recap`);
}

export function refreshRecap(
  identityId: string,
  rebuild = false
): Promise<{ ok: boolean }> {
  return postJson(`/api/identities/${encodeURIComponent(identityId)}/recap/refresh`, {
    rebuild,
  });
}

export function fetchUsage(identityId: string): Promise<Usage> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/usage`);
}

export function refreshUsage(
  identityId: string,
  rebuild = false
): Promise<{ ok: boolean }> {
  return postJson(`/api/identities/${encodeURIComponent(identityId)}/usage/refresh`, {
    rebuild,
  });
}

export function fetchThinkers(identityId: string): Promise<ThinkersStatus> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/thinkers`);
}

export function fetchThinkerSync(identityId: string): Promise<ThinkerSyncStatus> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinker-sync`
  );
}

export function pullThinkerSync(
  identityId: string,
  names: string[] = []
): Promise<ThinkerSyncResult> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinker-sync`,
    { names }
  );
}

export function startThinkers(
  identityId: string,
  names: string[] = []
): Promise<ControlResult> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinkers/start`,
    { names }
  );
}

export function stopThinkers(
  identityId: string,
  names: string[] = [],
  force = false
): Promise<ControlResult> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinkers/stop`,
    { names, force }
  );
}

export function stepThinker(
  identityId: string,
  name: string
): Promise<ControlResult> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinkers/${encodeURIComponent(name)}/step`,
    {}
  );
}

export function setThinkerEnabled(
  identityId: string,
  name: string,
  enabled: boolean
): Promise<{ ok: boolean; name: string; disabled: boolean; needs_restart?: boolean }> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/thinkers/${encodeURIComponent(name)}/${enabled ? "enable" : "disable"}`,
    {}
  );
}

export function fetchChat(
  identityId: string,
  tail = 200,
  withName?: string
): Promise<ChatLog> {
  const withParam = withName ? `&with=${encodeURIComponent(withName)}` : "";
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/chat?tail=${tail}${withParam}`
  );
}

export interface ChatSendResult {
  ok: boolean;
  from: string;
  to: string;
  /** 落盘消息的真实 step_id——前端按此对账（乐观气泡退场/结局关联）。 */
  step_id: string;
  /** 客户端幂等键回显（仅非空时后端才回）。 */
  client_message_id?: string;
}

/** 发送一条聊天消息；带 client_message_id 时重发同键同载荷返回原步骤
 * （幂等），同键不同内容后端 409。 */
export function sendChat(
  identityId: string,
  content: string,
  fromName: string,
  clientMessageId?: string
): Promise<ChatSendResult> {
  return postJson(`/api/identities/${encodeURIComponent(identityId)}/chat`, {
    content,
    from_name: fromName,
    ...(clientMessageId ? { client_message_id: clientMessageId } : {}),
  });
}

// ---- 任务（显式委托）：/api/identities/{id}/tasks ----

export interface TaskSubmitInput {
  content: string;
  fromName?: string;
  clientMessageId?: string;
  /** 来源聊天消息 step_id——「转为任务」保留来源关联。 */
  sourceStepId?: string;
}

export interface TaskCommandOptions {
  /** 基准 attempt；缺省由后端取当前代次（锁内校验防 TOCTOU）。 */
  attempt?: number;
  /** 幂等键：重发同键返回原收据。 */
  requestId?: string;
}

function taskSubmitBody(input: TaskSubmitInput): Record<string, unknown> {
  const body: Record<string, unknown> = { content: input.content };
  if (input.fromName) body.from_name = input.fromName;
  if (input.clientMessageId) body.client_message_id = input.clientMessageId;
  if (input.sourceStepId) body.source_step_id = input.sourceStepId;
  return body;
}

function taskCommandBody(opts?: TaskCommandOptions): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  if (opts?.attempt !== undefined) body.attempt = opts.attempt;
  if (opts?.requestId) body.request_id = opts.requestId;
  return body;
}

/** 任务列表（日志序，新的在后）。 */
export function fetchTasks(identityId: string): Promise<AgentTask[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/tasks`);
}

/** 单任务详情（完整 task_id；未知 404）。 */
export function fetchTask(
  identityId: string,
  taskId: string
): Promise<AgentTask> {
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/tasks/${encodeURIComponent(taskId)}`
  );
}

/** 提交显式委托；幂等语义与 CLI 一致（同键同载荷返回原任务）。 */
export function submitTask(
  identityId: string,
  input: TaskSubmitInput
): Promise<AgentTask> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/tasks`,
    taskSubmitBody(input)
  );
}

export function cancelTask(
  identityId: string,
  taskId: string,
  opts?: TaskCommandOptions
): Promise<AgentTask> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/tasks/${encodeURIComponent(taskId)}/cancel`,
    taskCommandBody(opts)
  );
}

export function retryTask(
  identityId: string,
  taskId: string,
  opts?: TaskCommandOptions
): Promise<AgentTask> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/tasks/${encodeURIComponent(taskId)}/retry`,
    taskCommandBody(opts)
  );
}

// ---- 执行授权（等待批准的脚本）：/api/identities/{id}/approvals ----

/** 等待批准的脚本列表（授权控制面，与 CLI approve 同一份事实）。 */
export function fetchApprovals(identityId: string): Promise<PendingApproval[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/approvals`);
}

/** 批准（approve=true）或拒绝一个待批脚本。决定写回控制面，等待方
 * （心智运行）消费后脚本才继续或终止——这里不做状态改写。 */
export function decideApproval(
  identityId: string,
  hash: string,
  approve: boolean
): Promise<{ hash: string; decision: string }> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/approvals/${encodeURIComponent(hash)}/${approve ? "approve" : "deny"}`,
    {}
  );
}

export function fetchPushKey(): Promise<{ key: string }> {
  return getJson("/api/push/key");
}

export function subscribePush(
  name: string,
  subscription: PushSubscriptionJSON
): Promise<{ ok: boolean; subscriptions: number }> {
  return postJson("/api/push/subscriptions", { name, subscription });
}

export function unsubscribePush(
  endpoint: string
): Promise<{ ok: boolean; removed: boolean }> {
  return postJson("/api/push/unsubscribe", { endpoint });
}

export function createIdentity(name: string): Promise<{ id: string; name: string }> {
  return postJson("/api/identities", { name });
}

export function killAll(dryRun: boolean): Promise<KillallResult> {
  return postJson("/api/killall", { dry_run: dryRun });
}

// 下载地址交给 downloadFile，以认证 fetch 获取文件，不把长期 Token 放进 URL。
export async function downloadFile(url: string, filename: string): Promise<void> {
  const response = await apiRequest(url);
  const objectUrl = URL.createObjectURL(await response.blob());
  const anchor = document.createElement("a");
  try {
    anchor.href = objectUrl;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
  } finally {
    anchor.remove();
    // 给浏览器启动保存操作的时间，同时限制大文件对象的驻留时间。
    setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
  }
}

export function exportIdentityUrl(identityId: string, soulOnly = false): string {
  const suffix = soulOnly ? "?soul_only=true" : "";
  return `${API_BASE}/api/identities/${encodeURIComponent(identityId)}/export${suffix}`;
}

export interface ExportJob {
  job_id: string;
  identity_id: string;
  status: "running" | "done" | "failed";
  started_at: string;
  soul_only: boolean;
  slim: boolean;
  seconds: number;
  size: number | null;
  filename: string | null;
  error: string | null;
  download_url?: string;
}

export function startExportJob(
  identityId: string,
  opts: { soulOnly: boolean; slim: boolean }
): Promise<ExportJob> {
  return postJson(
    `/api/identities/${encodeURIComponent(identityId)}/export-jobs`,
    { soul_only: opts.soulOnly, slim: opts.slim }
  );
}

export function fetchExportJobs(identityId: string): Promise<ExportJob[]> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/export-jobs`);
}

export function deleteExportJob(jobId: string): Promise<{ ok: boolean }> {
  return sendJson("DELETE", `/api/export-jobs/${encodeURIComponent(jobId)}`, undefined);
}

export function fetchExportJob(jobId: string): Promise<ExportJob> {
  return getJson(`/api/export-jobs/${encodeURIComponent(jobId)}`);
}

export function exportJobDownloadUrl(job: ExportJob): string {
  return `${API_BASE}/api/export-jobs/${encodeURIComponent(job.job_id)}/download`;
}

export function exportAllUrl(): string {
  return `${API_BASE}/api/export`;
}

export async function importIdentities(
  file: File,
  name?: string
): Promise<ImportResult> {
  const suffix = name ? `?name=${encodeURIComponent(name)}` : "";
  const response = await apiRequest(`${API_BASE}/api/identities/import${suffix}`, {
    method: "POST",
    headers: { "Content-Type": "application/gzip" },
    body: file,
  });
  return response.json() as Promise<ImportResult>;
}

export function fetchOpenRouterModels(): Promise<OpenRouterModels> {
  return getJson("/api/openrouter/models");
}

// ---- 多提供商配置：/api/identities/{id}/llm/* ----

/** GET /llm/providers：两级合并后的有效视图（含密钥命中状态）。 */
export function fetchLlmProviders(identityId: string): Promise<LlmProvidersView> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/llm/providers`);
}

/** PUT /llm/providers：保存身份级文档；profile.api_key 分流写入身份 .env。 */
export function saveLlmProviders(
  identityId: string,
  doc: LlmProvidersSaveInput
): Promise<LlmProvidersView> {
  return sendJson(
    "PUT",
    `/api/identities/${encodeURIComponent(identityId)}/llm/providers`,
    doc
  );
}

/** GET /llm/models：拉取某档案的模型目录（fresh 绕过 5 分钟缓存）。 */
export function fetchLlmModels(
  identityId: string,
  profile?: string,
  fresh = false
): Promise<LlmModelsProbe> {
  const params = new URLSearchParams();
  if (profile) params.set("profile", profile);
  if (fresh) params.set("fresh", "1");
  const qs = params.toString();
  return getJson(
    `/api/identities/${encodeURIComponent(identityId)}/llm/models${qs ? `?${qs}` : ""}`
  );
}

/** GET /llm/config：三档位有效解析（与 CLI 同一解析器）。 */
export function fetchLlmConfig(identityId: string): Promise<LlmConfigView> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/llm/config`);
}

export function fetchIdentityEnv(identityId: string): Promise<IdentityEnv> {
  return getJson(`/api/identities/${encodeURIComponent(identityId)}/env`);
}

export function putEnvVar(
  identityId: string,
  key: string,
  value: string
): Promise<EnvEntry> {
  return sendJson(
    "PUT",
    `/api/identities/${encodeURIComponent(identityId)}/env`,
    { key, value }
  );
}

export function deleteEnvVar(
  identityId: string,
  key: string
): Promise<{ ok: boolean; key: string }> {
  return sendJson(
    "DELETE",
    `/api/identities/${encodeURIComponent(identityId)}/env/${encodeURIComponent(key)}`,
    undefined
  );
}

export const IN_PROGRESS_POLL_MS = 2000;

export function pollWhileLive(live: boolean | undefined): number | false {
  return live ? IN_PROGRESS_POLL_MS : false;
}

// ---- 流式回复（SSE）：GET /api/identities/{id}/replies/stream ----

/** 非回环 Token 部署的 Bearer 凭据：优先取 URL ?token=（取到即存入
 * localStorage，刷新后仍有效），否则读 localStorage 的 mindloop-web-token。
 * 常规回环部署没有 token——返回空串，行为与现状一致（不带 Authorization）。 */
const WEB_TOKEN_KEY = "mindloop-web-token";
let sessionToken: string | undefined;
let needsAuth = false;
let credentialRevision = 0;
const authListeners = new Set<() => void>();

export function authRequired(): boolean {
  return needsAuth;
}

/** 不暴露 Token 的稳定快照；显式保存/清除凭据均可重新连接。 */
export function webCredentialRevision(): number {
  return credentialRevision;
}

export function subscribeAuth(listener: () => void): () => void {
  authListeners.add(listener);
  return () => { authListeners.delete(listener); };
}

function setAuthRequired(value: boolean) {
  if (needsAuth === value) return;
  needsAuth = value;
  for (const listener of authListeners) listener();
}

function clearUrlToken() {
  const url = new URL(window.location.href);
  if (!url.searchParams.has("token")) return;
  url.searchParams.delete("token");
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
}

export function setWebToken(token: string): void {
  if (typeof window === "undefined") return;
  sessionToken = token.trim();
  try {
    if (sessionToken) window.localStorage.setItem(WEB_TOKEN_KEY, sessionToken);
    else window.localStorage.removeItem(WEB_TOKEN_KEY);
  } catch {
    // 禁止本地存储时保留当前会话凭据，仍清理地址栏。
  }
  clearUrlToken();
  needsAuth = false;
  credentialRevision += 1;
  for (const listener of authListeners) listener();
}

export function webToken(): string {
  if (typeof window === "undefined") return "";
  const fromUrl = new URLSearchParams(window.location.search).get("token");
  if (fromUrl !== null) setWebToken(fromUrl);
  if (sessionToken !== undefined) return sessionToken;
  try {
    return window.localStorage.getItem(WEB_TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

export interface BusyThinker {
  thinker: string;
  wake: string;
  since: string;
}

export type ReplyStreamEvent =
  | { type: "status"; replying: boolean; reply_to: string }
  | { type: "delta"; reply_to: string; offset: number; text: string }
  | { type: "done"; reply_to: string }
  | { type: "working"; working: boolean; busy: BusyThinker[] }
  | {
      type: "step";
      step_id: string;
      /** 线上的步骤类型（reasoning/action/shell-output/…）；判别字段
       * type 已被事件名占用，故改名 step_type 承载。 */
      step_type: string;
      ts: string;
      excerpt: string;
      /** 任务/运行归因：任务步骤标记 task_id，本轮运行的步骤带 run_id
       * 与 attempt；旧后端/非任务步骤缺省归一为 null。 */
      task_id: string | null;
      run_id: string | null;
      attempt: number | null;
    };

/** delta 的 offset：服务端文件字节游标（UTF-8）。缺失或非法按重建快照
 * （0）处理——与快照帧同语义，不会把坏值当成大偏移误判为缺口。 */
function normalizeOffset(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? Math.floor(value)
    : 0;
}

/** 解析一条 SSE 载荷为 ReplyStreamEvent；未知事件名或坏 JSON 返回 null——
 * 协议演进（新增事件类型）不能让一个坏事件断开整条流。 */
function parseReplyStreamEvent(
  name: string,
  data: string
): ReplyStreamEvent | null {
  try {
    const payload = JSON.parse(data) as Record<string, unknown>;
    if (name === "status") {
      return {
        type: "status",
        replying: Boolean(payload.replying),
        reply_to: String(payload.reply_to ?? ""),
      };
    }
    if (name === "delta") {
      return {
        type: "delta",
        reply_to: String(payload.reply_to ?? ""),
        offset: normalizeOffset(payload.offset),
        text: String(payload.text ?? ""),
      };
    }
    if (name === "done") {
      return { type: "done", reply_to: String(payload.reply_to ?? "") };
    }
    if (name === "working") {
      const busy = Array.isArray(payload.busy) ? payload.busy : [];
      return {
        type: "working",
        working: Boolean(payload.working),
        busy: busy.map((entry) => {
          const e = entry as Record<string, unknown>;
          return {
            thinker: String(e.thinker ?? ""),
            wake: String(e.wake ?? ""),
            since: String(e.since ?? ""),
          };
        }),
      };
    }
    if (name === "step") {
      const optString = (key: string): string | null => {
        const value = payload[key];
        return typeof value === "string" && value !== "" ? value : null;
      };
      return {
        type: "step",
        step_id: String(payload.step_id ?? ""),
        step_type: String(payload.type ?? ""),
        ts: String(payload.ts ?? ""),
        excerpt: String(payload.excerpt ?? ""),
        task_id: optString("task_id"),
        run_id: optString("run_id"),
        attempt:
          typeof payload.attempt === "number" && Number.isFinite(payload.attempt)
            ? payload.attempt
            : null,
      };
    }
  } catch {
    // 坏 JSON：忽略该事件，流继续。
  }
  return null;
}

/** SSE 字段值：冒号后至多一个前导空格按规范剥掉，其余原样保留。 */
function fieldValue(line: string, field: string): string {
  const value = line.slice(field.length);
  return value.startsWith(" ") ? value.slice(1) : value;
}

/** openReplyStream 的传输选项。 */
export interface ReplyStreamOptions {
  /** 中断（卸载/凭据更换/缺口重同步）；透传给 fetch。 */
  signal?: AbortSignal;
  /** 最后处理的事件序号：>0 时以 Last-Event-ID 头请求续接，服务端从该
   * 序号之后补发；缺省/0 表示从头开始（拿状态快照）。 */
  lastEventId?: number;
  /** HTTP 建立成功（可读体就绪）后回调：连接可用性判定与失败计数复位。 */
  onOpen?: () => void;
  /** 每个带 id: 行的事件在 onEvent 之前回调——缺口帧的 onEvent 需要先
   * 作废刚记录的锚点再触发重同步，次序不能颠倒。 */
  onId?: (id: number) => void;
}

/** 打开回复流并逐事件回调，直到连接关闭、出错或 abort。按 SSE 规范解析
 * event:/data:/id: 行（多行 data 以 \n 连接）、忽略 `:` 注释行（15s ping
 * 保活）；事件以空行分界，流意外结束时也会派发已累积的未决事件。HTTP 非
 * 2xx 抛错（404 = 旧后端无此端点，由调用方按重连/退化策略处理）。
 * 用 fetch + ReadableStream 而非 EventSource：请求需带 Authorization 头
 * 以支持非回环 Token 部署，EventSource 无法自定义请求头。 */
export async function openReplyStream(
  identityId: string,
  onEvent: (event: ReplyStreamEvent) => void,
  opts: ReplyStreamOptions = {}
): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  if (opts.lastEventId !== undefined && opts.lastEventId > 0) {
    headers["Last-Event-ID"] = String(opts.lastEventId);
  }
  const response = await apiRequest(
    `${API_BASE}/api/identities/${encodeURIComponent(identityId)}/replies/stream`,
    { headers, signal: opts.signal }
  );
  if (!response.body) {
    throw new Error("流式响应没有可读体（response.body 为空）");
  }
  opts.onOpen?.();
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let eventName = "";
  const dataLines: string[] = [];
  // 当前事件块的 id 行值：分派时先于 onEvent 生效（见 onId 注释）。
  let eventId: number | null = null;
  const dispatch = () => {
    if (!eventName && dataLines.length === 0) {
      eventId = null;
      return;
    }
    const event = parseReplyStreamEvent(eventName, dataLines.join("\n"));
    const id = eventId;
    eventName = "";
    dataLines.length = 0;
    eventId = null;
    // 坏 JSON 的事件不推进锚点（可能被截断，续接时宁可重放）。
    if (event !== null && id !== null) opts.onId?.(id);
    if (event) onEvent(event);
  };
  const handleLine = (line: string) => {
    if (line === "") {
      dispatch();
      return;
    }
    if (line.startsWith(":")) return; // 注释行（ping 保活）
    if (line.startsWith("event:")) {
      eventName = fieldValue(line, "event:");
      return;
    }
    if (line.startsWith("data:")) {
      dataLines.push(fieldValue(line, "data:"));
      return;
    }
    if (line.startsWith("id:")) {
      const raw = fieldValue(line, "id:");
      const n = Number(raw);
      eventId = raw !== "" && Number.isInteger(n) && n >= 0 ? n : null;
      return;
    }
    // 其他 SSE 字段（retry:）本协议未用，忽略。
  };
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let idx: number;
    // 兼容 \n 与 \r\n 两种行界。
    while ((idx = buffer.indexOf("\n")) >= 0) {
      let line = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 1);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      handleLine(line);
    }
  }
  // 流收尾：冲洗解码器残留的多字节序列与未换行的最后一行，再派发未决事件。
  const tail = decoder.decode();
  if (tail) handleLine(tail);
  dispatch();
}
