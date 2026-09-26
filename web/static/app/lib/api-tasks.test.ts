// 任务面 API 客户端与 SSE 归因解析的契约测试：请求路径、body 形状
// （幂等键只在非空时出场）、响应解析（step_id / task 字段）与 step
// 事件的 task_id/run_id/attempt 归一（缺省 → null，不伪装成 0/""）。
//
// mock 模式与 reply-stream.test.tsx 一致：stubGlobal fetch + calls
// 记录器 + setWebToken("") 隔离凭据。

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  cancelTask,
  fetchTasks,
  openReplyStream,
  retryTask,
  sendChat,
  setWebToken,
  submitTask,
  type ReplyStreamEvent,
} from "~/lib/api";

const encoder = new TextEncoder();

interface RecordedCall {
  url: string;
  init?: RequestInit;
}

let calls: RecordedCall[] = [];
let responder: (call: RecordedCall) => unknown;

function jsonResponse(data: unknown): unknown {
  return { ok: true, status: 200, json: async () => data };
}

function doneStream(parts: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const part of parts) controller.enqueue(encoder.encode(part));
      controller.close();
    },
  });
}

function bodyOf(call: RecordedCall | undefined): Record<string, unknown> {
  return JSON.parse(String(call?.init?.body ?? "{}")) as Record<string, unknown>;
}

beforeEach(() => {
  setWebToken("");
  calls = [];
  responder = () => jsonResponse({});
  const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const call: RecordedCall = { url: String(url), init };
    calls.push(call);
    return responder(call);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sendChat 幂等发送", () => {
  it("client_message_id 进请求体，响应解析出 step_id 对账字段", async () => {
    responder = () =>
      jsonResponse({
        ok: true,
        from: "nick",
        to: "ada",
        step_id: "s1",
        client_message_id: "cid-1",
      });
    const result = await sendChat("ada", "你好", "nick", "cid-1");

    expect(calls[0]?.url).toBe("/api/identities/ada/chat");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(bodyOf(calls[0])).toEqual({
      content: "你好",
      from_name: "nick",
      client_message_id: "cid-1",
    });
    expect(result.step_id).toBe("s1");
    expect(result.client_message_id).toBe("cid-1");
  });

  it("不带幂等键时请求体不含该字段（旧后端兼容路径）", async () => {
    responder = () =>
      jsonResponse({ ok: true, from: "nick", to: "ada", step_id: "s2" });
    const result = await sendChat("ada", "在吗", "nick");

    expect(bodyOf(calls[0])).toEqual({ content: "在吗", from_name: "nick" });
    expect(result.client_message_id).toBeUndefined();
  });
});

describe("任务客户端", () => {
  it("fetchTasks GET /tasks 并解析任务数组", async () => {
    responder = () =>
      jsonResponse([
        {
          task_id: "t1",
          identity_id: "ada",
          from: "operator",
          client_message_id: "c1",
          content: "整理周报",
          status: "queued",
          attempt: 0,
          created_at: "2026-09-25T10:00:00Z",
          updated_at: "2026-09-25T10:00:00Z",
          events: [],
        },
      ]);
    const items = await fetchTasks("ada");

    expect(calls[0]?.url).toBe("/api/identities/ada/tasks");
    expect(calls[0]?.init?.method).toBeUndefined();
    expect(items).toHaveLength(1);
    expect(items[0]?.task_id).toBe("t1");
    expect(items[0]?.status).toBe("queued");
  });

  it("submitTask body 形状：content/from_name/client_message_id/source_step_id 仅带非空项", async () => {
    responder = () =>
      jsonResponse({ task_id: "t2", status: "queued", events: [] });
    await submitTask("ada", {
      content: "跑一次冒烟",
      fromName: "nick",
      clientMessageId: "cid-2",
      sourceStepId: "s9",
    });

    expect(calls[0]?.url).toBe("/api/identities/ada/tasks");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(bodyOf(calls[0])).toEqual({
      content: "跑一次冒烟",
      from_name: "nick",
      client_message_id: "cid-2",
      source_step_id: "s9",
    });

    await submitTask("ada", { content: "最小提交" });
    expect(bodyOf(calls[1])).toEqual({ content: "最小提交" });
  });

  it("cancelTask/retryTask POST 到子路径；默认空 body，显式 attempt 进 body", async () => {
    responder = () => jsonResponse({ task_id: "t3", status: "canceled", events: [] });
    await cancelTask("ada", "t3");

    expect(calls[0]?.url).toBe("/api/identities/ada/tasks/t3/cancel");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(bodyOf(calls[0])).toEqual({});

    await retryTask("ada", "t3", { attempt: 2, requestId: "rid-1" });
    expect(calls[1]?.url).toBe("/api/identities/ada/tasks/t3/retry");
    expect(bodyOf(calls[1])).toEqual({ attempt: 2, request_id: "rid-1" });
  });

  it("任务 ID 编入 URL 时转义，不破坏路径层级", async () => {
    responder = () => jsonResponse({ task_id: "a/b", status: "failed", events: [] });
    await retryTask("ada", "a/b");
    expect(calls[0]?.url).toBe("/api/identities/ada/tasks/a%2Fb/retry");
  });
});

describe("SSE step 事件的 task/run 归因", () => {
  it("解析 task_id/run_id/attempt；缺省归一为 null 而不是空串/0", async () => {
    const parts = [
      'event: step\ndata: {"step_id":"s1","type":"action","ts":"2026-09-25T10:00:00Z","excerpt":"跑脚本","task_id":"t-1","run_id":"r-1","attempt":2}\n\n',
      'event: step\ndata: {"step_id":"s2","type":"reasoning","ts":"2026-09-25T10:00:01Z","excerpt":"想想"}\n\n',
      'event: step\ndata: {"step_id":"s3","type":"final","ts":"2026-09-25T10:00:02Z","excerpt":"完成","task_id":"t-1","attempt":null}\n\n',
    ];
    responder = () => ({ ok: true, status: 200, body: doneStream(parts) });
    const events: ReplyStreamEvent[] = [];
    await openReplyStream("ada", (event) => events.push(event));

    expect(events).toHaveLength(3);
    expect(events[0]).toMatchObject({
      type: "step",
      step_id: "s1",
      task_id: "t-1",
      run_id: "r-1",
      attempt: 2,
    });
    expect(events[1]).toMatchObject({
      type: "step",
      step_id: "s2",
      task_id: null,
      run_id: null,
      attempt: null,
    });
    expect(events[2]).toMatchObject({
      type: "step",
      step_id: "s3",
      task_id: "t-1",
      attempt: null,
    });
  });
});
