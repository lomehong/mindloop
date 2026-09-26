// 幂等发送的行为测试：每条消息生成 client_message_id 并进请求体；失败
// 重试复用同一请求键（后端按键幂等返回原步骤，重发不落盘重复消息）；
// nonTaskActivity 把任务步骤从聊天进度数据流中剔除——身份其他任务/
// 活动不冒充聊天进度（反之任务页按 task/run 归因过滤）。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { setWebToken } from "~/lib/api";
import {
  CHAT_FAST_POLL_MS,
  CHAT_FAST_POLL_WINDOW_MS,
  CHAT_IDLE_POLL_MS,
} from "~/lib/polling";
import {
  chatPollInterval,
  nonTaskActivity,
  useChat,
  type StepActivity,
} from "~/lib/use-chat";

interface RecordedCall {
  url: string;
  init?: RequestInit;
}

let calls: RecordedCall[] = [];
let responder: (call: RecordedCall) => unknown;

function jsonResponse(data: unknown, status = 200): unknown {
  return {
    ok: status < 400,
    status,
    statusText: status < 400 ? "OK" : "Server Error",
    json: async () => data,
  };
}

function chatLog(): unknown {
  return jsonResponse({
    identity: { id: "ada", name: "ada" },
    live: false,
    messages: [],
    outcomes: {},
  });
}

function makeClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

function postBodies(): Record<string, unknown>[] {
  return calls
    .filter((call) => call.init?.method === "POST")
    .map((call) => JSON.parse(String(call.init?.body ?? "{}")) as Record<string, unknown>);
}

/** 聊天轮询次数：只数 GET（POST 发送不计入节奏断言）。 */
function chatGets(): number {
  return calls.filter(
    (call) => call.url.includes("/chat") && !call.init?.method
  ).length;
}

beforeEach(() => {
  setWebToken("");
  calls = [];
  responder = () => chatLog();
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

describe("useChat 幂等发送", () => {
  it("send 生成 client_message_id 进请求体，乐观气泡持有同一键", async () => {
    responder = (call) =>
      call.init?.method === "POST"
        ? jsonResponse({ ok: true, from: "nick", to: "ada", step_id: "s1" })
        : chatLog();
    const { result } = renderHook(
      () => useChat({ identityId: "ada", myName: "nick" }),
      { wrapper: makeWrapper(makeClient()) }
    );
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));

    act(() => {
      result.current.send("你好");
    });
    await waitFor(() => expect(postBodies()).toHaveLength(1));
    const body = postBodies()[0];
    expect(body?.content).toBe("你好");
    expect(typeof body?.client_message_id).toBe("string");
    expect(String(body?.client_message_id).length).toBeGreaterThan(0);

    await waitFor(() => expect(result.current.pending).toHaveLength(1));
    expect(result.current.pending[0]?.clientMessageId).toBe(
      body?.client_message_id
    );
  });

  it("失败重试复用同一 client_message_id（幂等键随消息走，不重新生成）", async () => {
    let posts = 0;
    responder = (call) => {
      if (call.init?.method === "POST") {
        posts += 1;
        if (posts === 1)
          return jsonResponse({ detail: { message: "落盘失败" } }, 500);
        return jsonResponse({ ok: true, from: "nick", to: "ada", step_id: "s9" });
      }
      return chatLog();
    };
    const { result } = renderHook(
      () => useChat({ identityId: "ada", myName: "nick" }),
      { wrapper: makeWrapper(makeClient()) }
    );
    act(() => {
      result.current.send("重试我");
    });
    await waitFor(() => expect(result.current.pending[0]?.failed).toBe(true));
    const firstCid = postBodies()[0]?.client_message_id;

    act(() => {
      result.current.retry(result.current.pending[0]!);
    });
    await waitFor(() => expect(postBodies()).toHaveLength(2));
    expect(postBodies()[1]?.client_message_id).toBe(firstCid);
  });
});

describe("nonTaskActivity", () => {
  it("剔除任务步骤、保留普通步骤（任务进度不冒充聊天进度）", () => {
    const step = (stepId: string, taskId: string | null): StepActivity => ({
      stepId,
      stepType: "action",
      ts: "2026-09-25T10:00:00Z",
      excerpt: stepId,
      arrivedAt: 0,
      taskId,
      runId: taskId ? "run-1" : null,
      attempt: taskId ? 1 : null,
    });
    const filtered = nonTaskActivity([
      step("s1", null),
      step("s2", "task-1"),
      step("s3", null),
    ]);
    expect(filtered.map((entry) => entry.stepId)).toEqual(["s1", "s3"]);
  });
});

describe("streamLiveRef 与轮询节奏", () => {
  it("chatPollInterval：健康流走慢节奏；断线后在发送窗口内恢复快轮询", () => {
    const now = 1_000_000;
    expect(chatPollInterval({ streamLive: true, sentAt: now - 10, now })).toBe(
      CHAT_IDLE_POLL_MS
    );
    expect(chatPollInterval({ streamLive: true, sentAt: null, now })).toBe(
      CHAT_IDLE_POLL_MS
    );
    expect(chatPollInterval({ streamLive: false, sentAt: now - 10, now })).toBe(
      CHAT_FAST_POLL_MS
    );
    expect(
      chatPollInterval({
        streamLive: false,
        sentAt: now - CHAT_FAST_POLL_WINDOW_MS,
        now,
      })
    ).toBe(CHAT_IDLE_POLL_MS);
    expect(chatPollInterval({ streamLive: false, sentAt: null, now })).toBe(
      CHAT_IDLE_POLL_MS
    );
  });

  it("健康 SSE 期间发送不触发快轮询；断线后轮询恢复（假定时器）", async () => {
    vi.useFakeTimers();
    // 发送返回 500：避免成功后 invalidate 补一次查询，干扰节奏断言。
    responder = (call) =>
      call.init?.method === "POST"
        ? jsonResponse({ detail: { message: "落盘失败" } }, 500)
        : chatLog();
    const { result } = renderHook(
      () => useChat({ identityId: "ada", myName: "nick" }),
      { wrapper: makeWrapper(makeClient()) }
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(chatGets()).toBe(1);

    // 健康流：useReplyStream 会把 live 镜像进 ref；发送后 700ms 快轮询
    // 被跳过（若按旧节奏 700/1400ms 两次跳升，这里会提前失败）。
    act(() => {
      result.current.streamLiveRef.current = true;
    });
    act(() => {
      result.current.send("你好");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(chatGets()).toBe(1);

    // 断线：置回 false 后下一节拍恢复轮询（含 700ms 快节奏）。
    act(() => {
      result.current.streamLiveRef.current = false;
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(chatGets()).toBeGreaterThan(1);
  });
});
