/** 场景流客户端（scene.md §3.2 前端侧）：订阅 /scene/stream，维护
 * 当前场景的折叠状态。服务端保证：订阅即全量快照帧（scene），此后
 * 增量 patch 帧（scene.patch，upsert/remove ops）；断线重连拿新快照，
 * 前端不做本地状态猜测——UI = f(scene)，状态永远来自服务端投影。 */

import { apiRequest, API_BASE } from "./api";

export type SceneIntent = "ambient" | "inform" | "confront";

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

type SceneFrame =
  | { type: "scene"; state: SceneState }
  | {
      type: "scene.patch";
      rev: number;
      ops: Array<
        | { op: "upsert"; surface: SceneSurface }
        | { op: "remove"; id: string }
      >;
    };

/** 打开场景流并逐帧回调折叠后的状态，直到连接关闭、出错或 abort。
 * SSE 解析与 api.ts 的 openReplyStream 同规范（event:/data: 行、`:`
 * 注释 ping、空行分界），事件类型不同（scene / scene.patch）故独立
 * 实现——两处解析各自轻量，泛化抽象反而把 replies 的类型耦合进来。
 * 断线（网络错误/非 2xx）抛错，由 useSceneStream 的重连循环处理。 */
export async function openSceneStream(
  identityId: string,
  onState: (update: (prev: SceneState) => SceneState) => void,
  opts: { signal?: AbortSignal } = {}
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

  const apply = (frame: SceneFrame) => {
    if (frame.type === "scene") {
      onState(() => frame.state);
      return;
    }
    // patch：upsert 整体替换（无字段级深合并——协议约束，service 端
    // 已保证 surface 是不可分割整体）；remove 缺失忽略。
    // rev 不做间隙检测：SSE 有序 + 断线即重发全量，间隙不会发生。
    onState((prev) => {
      let surfaces = prev.surfaces;
      for (const op of frame.ops) {
        if (op.op === "upsert") {
          surfaces = surfaces.some((s) => s.id === op.surface.id)
            ? surfaces.map((s) => (s.id === op.surface.id ? op.surface : s))
            : [...surfaces, op.surface];
        } else {
          surfaces = surfaces.filter((s) => s.id !== op.id);
        }
      }
      return { rev: frame.rev, surfaces };
    });
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
