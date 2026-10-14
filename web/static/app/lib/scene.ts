/** 场景流客户端（scene.md §3.2 前端侧）：订阅 /scene/stream，维护
 * 当前场景的折叠状态。服务端保证：订阅即全量快照帧（scene），此后
 * 增量 patch 帧（scene.patch，upsert/remove ops）；断线重连拿新快照，
 * 前端不做本地状态猜测——UI = f(scene)，状态永远来自服务端投影。 */

import { apiRequest, API_BASE } from "./api";

export type SceneIntent = "ambient" | "inform" | "confront";

/** remove 的生命周期括号（theater.md §16.7）：投影端按 reason 选退场
 * 动效——ttl 蒸发沉淀 / superseded 替换淡切 / done 收工 / dismissed
 * 手动收起。 */
export type SceneExitReason = "ttl" | "superseded" | "done" | "dismissed";

/** 场景单元：协议字段是纯数据（无样式），结构由 kind 词汇表定义。 */
export type SceneSurface = {
  id: string;
  kind: string;
  intent?: SceneIntent;
  focus?: boolean;
  summary?: string;
  data?: Record<string, unknown>;
};

export type SceneState = {
  rev: number;
  surfaces: SceneSurface[];
};

/** 场景流事件（theater.md §11 E1/E3 的派生源）：op 在状态更新前
 * 发出，remove 事件携带被撤卡的完整内容（退场动画要用它渲染）。 */
export type SceneStreamEvent =
  | {
      type: "upsert";
      id: string;
      surface: SceneSurface;
      batchId?: string;
      /** 批内序号（0 起）——enter 动画 stagger = index × 40ms。 */
      batchIndex: number;
      t0: number;
    }
  | {
      type: "remove";
      id: string;
      surface: SceneSurface;
      reason: SceneExitReason;
      t0: number;
    };

type SceneFrame =
  | { type: "scene"; state: SceneState }
  | {
      type: "scene.patch";
      rev: number;
      /** choreography 批号（theater.md §16.7）：同批多卡单帧落位。 */
      batchId?: string;
      /** 批内 stagger 步长 ms（仅多卡批携带）。 */
      staggerHint?: number;
      ops: Array<
        | { op: "upsert"; surface: SceneSurface }
        | { op: "remove"; id: string; exit_reason?: SceneExitReason }
      >;
    };

/** 打开场景流并逐帧回调折叠后的状态，直到连接关闭、出错或 abort。
 * onEvent 可选：每次 upsert/remove 在状态更新前发出（E1 推卡脉冲 /
 * 按 reason 退场动画的派生源；remove 事件携带被撤卡的完整内容）。
 * SSE 解析与 api.ts 的 openReplyStream 同规范（event:/data: 行、`:`
 * 注释 ping、空行分界），事件类型不同（scene / scene.patch）故独立
 * 实现——两处解析各自轻量，泛化抽象反而把 replies 的类型耦合进来。
 * 断线（网络错误/非 2xx）抛错，由 useSceneStream 的重连循环处理。 */
export async function openSceneStream(
  identityId: string,
  onState: (update: (prev: SceneState) => SceneState) => void,
  opts: {
    signal?: AbortSignal;
    onEvent?: (ev: SceneStreamEvent) => void;
  } = {}
): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  const response = await apiRequest(
    `${API_BASE}/api/identities/${encodeURIComponent(identityId)}/scene/stream`,
    { headers, signal: opts.signal }
  );
  if (!response.body) {
    throw new Error("场景流没有可读体（response.body 为空）");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let eventName = "";
  const dataLines: string[] = [];
  // 本地镜像：op 级事件需要"被撤卡的完整内容"，从镜像取——不在
  // onState 的 updater 里取（updater 必须纯，StrictMode 双跑会重复
  // 发事件）。
  let cur: SceneState = { rev: 0, surfaces: [] };

  const apply = (frame: SceneFrame) => {
    if (frame.type === "scene") {
      cur = frame.state;
      onState(() => frame.state);
      return;
    }
    // patch：upsert 整体替换（无字段级深合并——协议约束，service 端
    // 已保证 surface 是不可分割整体）；remove 缺失忽略。
    // rev 不做间隙检测：SSE 有序 + 断线即重发全量，间隙不会发生。
    const events: SceneStreamEvent[] = [];
    let surfaces = cur.surfaces;
    let upsertIndex = 0;
    for (const op of frame.ops) {
      if (op.op === "upsert") {
        events.push({
          type: "upsert",
          id: op.surface.id,
          surface: op.surface,
          batchId: frame.batchId,
          batchIndex: upsertIndex++,
          t0: Date.now(),
        });
        surfaces = surfaces.some((s) => s.id === op.surface.id)
          ? surfaces.map((s) => (s.id === op.surface.id ? op.surface : s))
          : [...surfaces, op.surface];
      } else {
        const gone = surfaces.find((s) => s.id === op.id);
        if (gone) {
          events.push({
            type: "remove",
            id: op.id,
            surface: gone,
            reason: op.exit_reason ?? "dismissed",
            t0: Date.now(),
          });
        }
        surfaces = surfaces.filter((s) => s.id !== op.id);
      }
    }
    cur = { rev: frame.rev, surfaces };
    // 事件先于状态：处理器可在状态落地前捕获被撤卡的内容。
    if (opts.onEvent) for (const ev of events) opts.onEvent(ev);
    onState(() => ({ rev: frame.rev, surfaces }));
  };

  const dispatch = () => {
    if (!eventName || dataLines.length === 0) {
      eventName = "";
      dataLines.length = 0;
      return;
    }
    const raw = dataLines.join("\n");
    eventName = "";
    dataLines.length = 0;
    try {
      const parsed = JSON.parse(raw) as SceneFrame;
      if (parsed.type === "scene" || parsed.type === "scene.patch") apply(parsed);
      // 未知帧类型忽略（向前兼容，对应协议的"未知 type 必须忽略"）。
    } catch {
      // 坏 JSON：丢帧。重连后全量快照兜底，不累积错状态。
    }
  };

  const handleLine = (line: string) => {
    if (line === "") {
      dispatch();
      return;
    }
    if (line.startsWith(":")) return; // ping 注释
    if (line.startsWith("event:")) {
      eventName = line.slice(6).trim();
      return;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trimStart());
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let idx: number;
    while ((idx = buffer.indexOf("\n")) >= 0) {
      handleLine(buffer.slice(0, idx).replace(/\r$/, ""));
      buffer = buffer.slice(idx + 1);
    }
  }
  handleLine(buffer); // 流尾的未决事件
  dispatch();
}

/** intent 上行（scene.md §3.4）：surface + name + 载荷。choice 卡
 * 的 select 由后端换回审批 hash 走 policy.Decide；其余意图只落轨迹
 * 作上下文。 */
export async function postSceneIntent(
  identityId: string,
  intent: { surface: string; name: string; data?: Record<string, unknown> }
): Promise<void> {
  const response = await apiRequest(
    `${API_BASE}/api/identities/${encodeURIComponent(identityId)}/scene/intent`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(intent),
    }
  );
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`intent 上行失败（${response.status}）：${text.slice(0, 200)}`);
  }
}
