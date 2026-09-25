import type {
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
  LlmHealth,
  LlmProbeResult,
  IdentityStatus,
  KillallResult,
  LogInfo,
  LogTail,
  MemoryInfo,
  Mindlog,
  MindlogSearchResult,
  OpenRouterModels,
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

async function getJson<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`);
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  return response.json() as Promise<T>;
}

async function sendJson<T>(
  method: string,
  path: string,
  body: unknown
): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
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

export function sendChat(
  identityId: string,
  content: string,
  fromName: string
): Promise<{ ok: boolean; from: string; to: string }> {
  return postJson(`/api/identities/${encodeURIComponent(identityId)}/chat`, {
    content,
    from_name: fromName,
  });
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

// Export endpoints are plain downloads — link to them, don't fetch.
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
  return `${API_BASE}${job.download_url ?? `/api/export-jobs/${job.job_id}/download`}`;
}

export function exportAllUrl(): string {
  return `${API_BASE}/api/export`;
}

export async function importIdentities(
  file: File,
  name?: string
): Promise<ImportResult> {
  const suffix = name ? `?name=${encodeURIComponent(name)}` : "";
  const response = await fetch(`${API_BASE}/api/identities/import${suffix}`, {
    method: "POST",
    headers: { "Content-Type": "application/gzip" },
    body: file,
  });
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const data = await response.json();
      if (typeof data?.detail === "string") message = data.detail;
      else if (data?.detail?.message) message = data.detail.message;
    } catch {
      // keep default message
    }
    throw new Error(message);
  }
  return response.json() as Promise<ImportResult>;
}

export function fetchOpenRouterModels(): Promise<OpenRouterModels> {
  return getJson("/api/openrouter/models");
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

export function webToken(): string {
  if (typeof window === "undefined") return "";
  try {
    const fromUrl = new URLSearchParams(window.location.search).get("token");
    if (fromUrl) {
      window.localStorage.setItem(WEB_TOKEN_KEY, fromUrl);
      return fromUrl;
    }
    return window.localStorage.getItem(WEB_TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

function authHeaders(): Record<string, string> {
  const token = webToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

export interface BusyThinker {
  thinker: string;
  wake: string;
  since: string;
}

export type ReplyStreamEvent =
  | { type: "status"; replying: boolean; reply_to: string }
  | { type: "delta"; reply_to: string; text: string }
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
    };

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
      return {
        type: "step",
        step_id: String(payload.step_id ?? ""),
        step_type: String(payload.type ?? ""),
        ts: String(payload.ts ?? ""),
        excerpt: String(payload.excerpt ?? ""),
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

/** 打开回复流并逐事件回调，直到连接关闭、出错或 abort。按 SSE 规范解析
 * event:/data: 行（多行 data 以 \n 连接）、忽略 `:` 注释行（15s ping 保
 * 活）；事件以空行分界，流意外结束时也会派发已累积的未决事件。HTTP 非
 * 2xx 抛错（404 = 旧后端无此端点，由调用方按重连/退化策略处理）。
 * 用 fetch + ReadableStream 而非 EventSource：请求需带 Authorization 头
 * 以支持非回环 Token 部署，EventSource 无法自定义请求头。 */
export async function openReplyStream(
  identityId: string,
  onEvent: (event: ReplyStreamEvent) => void,
  signal?: AbortSignal,
  onOpen?: () => void
): Promise<void> {
  const response = await fetch(
    `${API_BASE}/api/identities/${encodeURIComponent(identityId)}/replies/stream`,
    { headers: { Accept: "text/event-stream", ...authHeaders() }, signal }
  );
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  if (!response.body) {
    throw new Error("流式响应没有可读体（response.body 为空）");
  }
  onOpen?.();
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let eventName = "";
  const dataLines: string[] = [];
  const dispatch = () => {
    if (!eventName && dataLines.length === 0) return;
    const event = parseReplyStreamEvent(eventName, dataLines.join("\n"));
    eventName = "";
    dataLines.length = 0;
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
    // 其他 SSE 字段（id:/retry:）本协议未用，忽略。
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
