// 流式回复消费层（useReplyStream）的行为测试：用 mock fetch 的
// ReadableStream 分块喂 SSE 文本，验证解析（跨 chunk 切割 / \r\n 行界 /
// ping 注释行 / 坏事件容错）、渐进状态、done 后 invalidate、断线重连、
// 连续失败退化与卸载 abort。
//
// 确定性要点：①中间态断言必须配合「保持打开的流」（事件入队后不
// close），否则同一批微任务里的 delta→done 会被 React 批处理成一个渲
// 染，中间态不可观测；②连接干净关闭时 hook 会按设计置 live=false 并重
// 连；③mock fetch 统一走 calls 记录器，每个用例只替换 responder；
// ④凡是断言依赖定时器的用例（linger 清空、退避重连、退化停手）一律用
// vi.useFakeTimers + advanceTimersByTimeAsync 确定性推进——全量并行跑
// 时墙钟定时器会偶发超窗，绝不 sleep 等真实时间。

import { act, renderHook, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { useReplyStream } from "~/lib/use-chat";
import { setWebToken } from "~/lib/api";

const encoder = new TextEncoder();

// 产品的 linger 窗口是 400ms（use-chat.ts ACTIVITY_LINGER_MS，未导出）；
// 测试里推进 450ms = 窗口 + 余量。
const LINGER_WINDOW_PLUS_SLACK_MS = 450;

interface RecordedCall {
  url: string;
  init?: RequestInit;
}

let calls: RecordedCall[] = [];
let responder: (call: RecordedCall) => unknown;

/** 事件全部入队后保持打开（不 close）——模拟长连接，中间态可稳定断言。 */
function holdStream(parts: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const part of parts) controller.enqueue(encoder.encode(part));
      // 不关闭。
    },
  });
}

/** 事件全部入队后立即关闭——只断言最终态。 */
function doneStream(parts: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const part of parts) controller.enqueue(encoder.encode(part));
      controller.close();
    },
  });
}

/** 与请求信号联动：abort 时让读取端报错——真实 fetch 被 abort 时正是
 * 这种语义（测试桩必须自实现，mock fetch 不会自动联动信号）。 */
function abortableStream(
  parts: string[],
  signal?: AbortSignal | null
): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const part of parts) controller.enqueue(encoder.encode(part));
      signal?.addEventListener("abort", () => {
        controller.error(new DOMException("Aborted", "AbortError"));
      });
    },
  });
}

function okResponse(body: ReadableStream<Uint8Array>): unknown {
  return { ok: true, status: 200, body };
}

function notFoundResponse(): unknown {
  return {
    ok: false,
    status: 404,
    statusText: "Not Found",
    json: async () => ({ detail: { message: "404 Not Found" } }),
  };
}

function makeClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

beforeEach(() => {
  setWebToken("");
  calls = [];
  responder = () => notFoundResponse();
  const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const call: RecordedCall = { url: String(url), init };
    calls.push(call);
    return responder(call);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("useReplyStream", () => {
  it("解析跨 chunk 切割的 SSE（\\r\\n 行界、ping 注释行）并渐进更新累积全文", async () => {
    responder = () =>
      okResponse(
        holdStream([
          "event: stat",
          'us\r\ndata: {"replying":tru',
          'e,"reply_to":"m1"}\n\n: ping\n\n',
          'event: delta\ndata: {"reply_to":"m1","text":"你好，世界"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.live).toBe(true));
    await waitFor(() =>
      expect(result.current.reply?.text).toBe("你好，世界")
    );
    expect(result.current.reply?.replyTo).toBe("m1");
    expect(result.current.degraded).toBe(false);
    expect(calls[0]?.url).toContain("/api/identities/ada/replies/stream");
  });

  it("done 事件清空流式气泡并 invalidate chat 查询（正式消息取回）", async () => {
    const client = makeClient();
    client.setQueryData(["chat", "ada", "nick"], { messages: [] });
    responder = () =>
      okResponse(
        doneStream([
          'event: delta\ndata: {"reply_to":"m1","text":"完整回复"}\n\n',
          'event: done\ndata: {"reply_to":"m1"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(client) }
    );

    await waitFor(() => expect(result.current.reply).toBeNull());
    await waitFor(() =>
      expect(client.getQueryState(["chat", "ada", "nick"])?.isInvalidated).toBe(
        true
      )
    );
  });

  it("容忍坏 JSON 与未知事件（流不断开，后续事件照常处理）", async () => {
    responder = () =>
      okResponse(
        holdStream([
          "event: delta\ndata: not-json\n\n",
          'event: unknown\ndata: {"future":true}\n\n',
          'event: delta\ndata: {"reply_to":"m1","text":"恢复"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.reply?.text).toBe("恢复"));
  });

  it("首次失败后退避重连，成功建立连接即复位失败计数（假定时器）", async () => {
    vi.useFakeTimers();
    let attempt = 0;
    responder = () => {
      attempt += 1;
      if (attempt === 1) return notFoundResponse();
      return okResponse(
        holdStream(['event: status\ndata: {"replying":false,"reply_to":""}\n\n'])
      );
    };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    // 推进越过 1ms 退避并冲洗读取链：第二次尝试成功且连接保持打开。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(result.current.live).toBe(true);
    expect(calls.length).toBe(2);
    expect(result.current.degraded).toBe(false);
  });

  it("连续失败 3 次后退化为纯轮询并彻底停止重连（假定时器）", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    // 三次尝试（初始 + 1ms/2ms 退避）全部 404 → degraded。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(result.current.degraded).toBe(true);
    expect(calls.length).toBe(3);
    // 长时间推进：没有任何重连发生（纯轮询模式，重连彻底停手）。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(calls.length).toBe(3);
  });

  it("401 降级后更新凭据恢复 SSE，不需要切换身份或重放写请求", async () => {
    vi.useFakeTimers();
    responder = (call) => new Headers(call.init?.headers).get("Authorization") === "Bearer refreshed-token"
      ? okResponse(holdStream(['event: status\ndata: {"replying":false,"reply_to":""}\n\n']))
      : Response.json({}, { status: 401 });
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(result.current.degraded).toBe(true);
    await act(async () => { setWebToken("refreshed-token"); });
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(result.current.live).toBe(true);
    expect(result.current.degraded).toBe(false);
    expect(calls.length).toBe(4);
    expect(calls.every((call) => call.url === "/api/identities/ada/replies/stream" && !call.init?.method)).toBe(true);
  });

  it("主动更换有效凭据会终止旧流，旧流迟归内容不能覆盖新回复", async () => {
    let oldStream: ReadableStreamDefaultController<Uint8Array>;
    responder = (call) => {
      if (new Headers(call.init?.headers).get("Authorization") === "Bearer replacement-token") {
        return okResponse(holdStream([
          'event: status\ndata: {"replying":true,"reply_to":"new"}\n\n',
          'event: delta\ndata: {"reply_to":"new","text":"新凭据回复"}\n\n',
        ]));
      }
      return okResponse(new ReadableStream<Uint8Array>({
        start(controller) {
          oldStream = controller;
          controller.enqueue(encoder.encode('event: status\ndata: {"replying":false,"reply_to":""}\n\n'));
        },
      }));
    };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada" }),
      { wrapper: makeWrapper(makeClient()) }
    );
    await waitFor(() => expect(result.current.live).toBe(true));
    await act(async () => { setWebToken("replacement-token"); });
    await waitFor(() => expect(result.current.reply?.text).toBe("新凭据回复"));
    expect(calls[0]?.init?.signal?.aborted).toBe(true);
    await act(async () => {
      oldStream.enqueue(encoder.encode('event: delta\ndata: {"reply_to":"old","text":"过期内容"}\n\n'));
      oldStream.close();
    });
    expect(result.current.reply?.text).toBe("新凭据回复");
    expect(calls.length).toBe(2);
  });

  it("step 事件进入 activity（按到达顺序），超过 6 条裁掉最旧的", async () => {
    const events: string[] = [];
    for (let i = 1; i <= 8; i++) {
      events.push(
        `event: step\ndata: {"step_id":"s${i}","type":"action","ts":"2026-09-03T12:0${i}:00Z","excerpt":"步骤 ${i}"}\n\n`
      );
    }
    responder = () => okResponse(holdStream(events));
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.activity.length).toBe(6));
    // 保留的是最近 6 条（s3..s8），按到达顺序排列。
    expect(result.current.activity.map((a) => a.stepId)).toEqual([
      "s3",
      "s4",
      "s5",
      "s6",
      "s7",
      "s8",
    ]);
    expect(result.current.activity[0]?.stepType).toBe("action");
  });

  it("step 事件的 task/run 归因进入 activity；缺省归一 null", async () => {
    responder = () =>
      okResponse(
        holdStream([
          'event: step\ndata: {"step_id":"t1","type":"action","ts":"2026-09-25T10:00:00Z","excerpt":"任务步","task_id":"task-1","run_id":"run-1","attempt":3}\n\n',
          'event: step\ndata: {"step_id":"m1","type":"reasoning","ts":"2026-09-25T10:00:01Z","excerpt":"聊天步"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.activity.length).toBe(2));
    expect(result.current.activity[0]).toMatchObject({
      stepId: "t1",
      taskId: "task-1",
      runId: "run-1",
      attempt: 3,
    });
    expect(result.current.activity[1]).toMatchObject({
      stepId: "m1",
      taskId: null,
      runId: null,
      attempt: null,
    });
  });

  it("任务步骤不计入 stepTotal（聊天步数不被任务进度顶替）", async () => {
    responder = () =>
      okResponse(
        holdStream([
          'event: step\ndata: {"step_id":"t1","type":"action","ts":"2026-09-25T10:00:00Z","excerpt":"任务步","task_id":"task-1"}\n\n',
          'event: step\ndata: {"step_id":"m1","type":"reasoning","ts":"2026-09-25T10:00:01Z","excerpt":"聊天步一"}\n\n',
          'event: step\ndata: {"step_id":"m2","type":"final","ts":"2026-09-25T10:00:02Z","excerpt":"聊天步二"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.activity.length).toBe(3));
    expect(result.current.stepTotal).toBe(2);
  });

  it("working 事件驱动 working/busy；working=false 后延迟清空 activity（假定时器）", async () => {
    vi.useFakeTimers();
    let served = false;
    responder = () => {
      if (!served) {
        served = true;
        return okResponse(
          doneStream([
            'event: working\ndata: {"working":true,"busy":[{"thinker":"responder","wake":"w1","since":"s1"}]}\n\n',
            'event: step\ndata: {"step_id":"a1","type":"reasoning","ts":"2026-09-03T12:00:00Z","excerpt":"想一想"}\n\n',
            'event: step\ndata: {"step_id":"a2","type":"error","ts":"2026-09-03T12:01:00Z","excerpt":"出错了"}\n\n',
            'event: working\ndata: {"working":false,"busy":[]}\n\n',
          ])
        );
      }
      // 重连后的挂起连接：不重放事件，避免干扰清空窗口的断言。
      return okResponse(holdStream([]));
    };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    // 冲洗微任务直到两个 step 事件落地（假定时器不冻结 Promise；初始
    // working 恰为 false，不能用 working 作冲洗条件）。
    await act(async () => {
      for (let i = 0; i < 200 && result.current.activity.length < 2; i++) {
        await Promise.resolve();
      }
    });
    // 中间态：working=false 已到达，但 400ms linger 计时器被假定时器
    // 冻结——activity 稳定保留（淡出窗口）。
    expect(result.current.working).toBe(false);
    expect(result.current.busy).toEqual([]);
    expect(result.current.activity.map((a) => a.stepId)).toEqual([
      "a1",
      "a2",
    ]);
    // 推进越过 linger 窗口（顺带触发 1ms 重连退避，挂起连接无事件）：
    // activity 清空，卡片退场。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(LINGER_WINDOW_PLUS_SLACK_MS);
    });
    expect(result.current.activity.length).toBe(0);
  });

  it("卸载时 abort 在途流（身份切换同理）", async () => {
    responder = () => okResponse(holdStream([])); // 永不关闭
    const { unmount } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(calls.length).toBe(1));
    const signal = calls[0]?.init?.signal;
    expect(signal).toBeDefined();
    expect(signal?.aborted).toBe(false);
    unmount();
    expect(signal?.aborted).toBe(true);
  });
});

describe("增量 delta、Last-Event-ID 与服务端重同步", () => {
  it("delta 按 offset 拼接（偏移是 UTF-8 字节而非字符数）", async () => {
    responder = () =>
      okResponse(
        holdStream([
          'event: delta\ndata: {"reply_to":"m1","offset":0,"text":"你好"}\n\n',
          'event: delta\ndata: {"reply_to":"m1","offset":6,"text":"，世界"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.reply?.text).toBe("你好，世界"));
  });

  it("offset 小于已收字节数：截断到该字节边界再追加（部分重写语义）", async () => {
    responder = () =>
      okResponse(
        holdStream([
          'event: delta\ndata: {"reply_to":"m1","offset":0,"text":"第一段"}\n\n',
          'event: delta\ndata: {"reply_to":"m1","offset":3,"text":"小节"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    // 「第一段」是 9 字节；offset=3 落在「第」之后（3 字节边界）。
    await waitFor(() => expect(result.current.reply?.text).toBe("第小节"));
  });

  it("offset=0 是重建快照：整段替换而非追加", async () => {
    responder = () =>
      okResponse(
        holdStream([
          'event: delta\ndata: {"reply_to":"m1","offset":0,"text":"旧的长文本"}\n\n',
          'event: delta\ndata: {"reply_to":"m1","offset":0,"text":"新文本"}\n\n',
        ])
      );
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.reply?.text).toBe("新文本"));
  });

  it("断线重连按 id 行携带 Last-Event-ID，续接不重放", async () => {
    let attempt = 0;
    responder = () => {
      attempt += 1;
      if (attempt === 1) {
        return okResponse(
          doneStream([
            'id: 1\nevent: delta\ndata: {"reply_to":"m1","offset":0,"text":"甲"}\n\n',
            'id: 2\nevent: delta\ndata: {"reply_to":"m1","offset":3,"text":"乙"}\n\n',
          ])
        );
      }
      return okResponse(holdStream([]));
    };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.reply?.text).toBe("甲乙"));
    await waitFor(() => expect(calls.length).toBe(2));
    expect(new Headers(calls[0]?.init?.headers).get("Last-Event-ID")).toBeNull();
    expect(new Headers(calls[1]?.init?.headers).get("Last-Event-ID")).toBe("2");
  });

  it("偏移缺口触发重同步：清空锚点重连取快照重建", async () => {
    let attempt = 0;
    responder = (call) => {
      attempt += 1;
      if (attempt === 1) {
        return okResponse(
          abortableStream(
            [
              'id: 1\nevent: delta\ndata: {"reply_to":"m1","offset":0,"text":"前半"}\n\n',
              'id: 2\nevent: delta\ndata: {"reply_to":"m1","offset":99,"text":"缺口后的片段"}\n\n',
            ],
            call.init?.signal
          )
        );
      }
      return okResponse(
        holdStream([
          'event: delta\ndata: {"reply_to":"m1","offset":0,"text":"前半后半完整快照"}\n\n',
        ])
      );
    };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    // offset=99 越过已收 6 字节：不拼接缺片段，作废锚点重连；快照帧
    // （无 id 行）不带 Last-Event-ID。
    await waitFor(() =>
      expect(result.current.reply?.text).toBe("前半后半完整快照")
    );
    expect(calls.length).toBe(2);
    expect(new Headers(calls[1]?.init?.headers).get("Last-Event-ID")).toBeNull();
  });

  it("live 状态镜像到 liveRef（健康 SSE 期间跳过快轮询的依据）", async () => {
    responder = () =>
      okResponse(
        holdStream(['event: status\ndata: {"replying":true,"reply_to":"m1"}\n\n'])
      );
    const liveRef = { current: false };
    const { result } = renderHook(
      () => useReplyStream({ identityId: "ada", liveRef, retryBaseMs: 1 }),
      { wrapper: makeWrapper(makeClient()) }
    );

    await waitFor(() => expect(result.current.live).toBe(true));
    expect(liveRef.current).toBe(true);
  });
});
