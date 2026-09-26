// The phone composer keeps Return as a newline and uses the Send button.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AgentTask,
  ChatLog,
  ChatMessage,
  Config,
  IdentityActivity,
  ThinkersStatus,
} from "~/lib/types";
import { setPwaName } from "~/lib/pwa";
import TalkChat from "~/routes/talk-chat";

Element.prototype.scrollIntoView ??= () => {};

vi.mock("~/components/push-bell", () => ({ PushBell: () => null }));

const sendChat = vi.fn(
  async (_identityId: string, _content: string, from: string) => ({
    ok: true,
    from,
    to: "ada",
    step_id: "s-mock",
  })
);

function chatLog(messages: ChatMessage[] = []): ChatLog {
  return {
    identity: { id: "ada", name: "ada" },
    live: false,
    messages,
    outcomes: {},
  };
}

const fetchChat = vi.fn(async (): Promise<ChatLog> => chatLog());

const submitTask = vi.fn(
  async (
    _identityId: string,
    input: { content: string }
  ): Promise<AgentTask> => ({
    task_id: "t-1",
    identity_id: "ada",
    from: "pwa-nick",
    client_message_id: "cid-1",
    content: input.content,
    status: "queued",
    attempt: 1,
    created_at: "2026-09-25T10:00:00Z",
    updated_at: "2026-09-25T10:00:00Z",
    events: [],
  })
);

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchConfig: vi.fn(
      async (): Promise<Config> => ({
        root: "/root",
        version: "0",
        controls_enabled: true,
        self_update_enabled: false,
        default_send_from: "you",
        git_commit: null,
        git_branch: null,
      })
    ),
    fetchActivity: vi.fn(
      async (): Promise<IdentityActivity> => ({
        state: "idle",
        dispatcher_running: true,
        busy_thinkers: [],
        last_step_ts: null,
        last_step_age_s: null,
        run_seconds: null,
        stall_after_s: 60,
        cadence_s: null,
      })
    ),
    fetchChat: (...args: Parameters<typeof fetchChat>) => fetchChat(...args),
    submitTask: (...args: Parameters<typeof submitTask>) => submitTask(...args),
    fetchThinkers: vi.fn(
      async (): Promise<ThinkersStatus> => ({
        identity: { id: "ada", name: "ada" },
        dispatcher: { running: true, pid: 1 },
        active_thinkers: 0,
        thinkers_total: 0,
        thinkers_disabled: 0,
        thinkers: [],
      })
    ),
    sendChat: (...args: Parameters<typeof sendChat>) => sendChat(...args),
  };
});

function renderTalkChat() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/talk/ada"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/talk/:identityId" element={<TalkChat />} />
          <Route
            path="/talk/:identityId/tasks"
            element={<div>任务页占位</div>}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  sendChat.mockClear();
  submitTask.mockClear();
  fetchChat.mockReset();
  fetchChat.mockResolvedValue(chatLog());
  setPwaName("nick");
});
afterEach(cleanup);

describe("phone chat composer", () => {
  it("leaves Return for newlines and sends multiline text with the button", async () => {
    renderTalkChat();
    const box = await screen.findByPlaceholderText("给 ada 发消息…");
    fireEvent.change(box, { target: { value: "line one" } });

    const allowedNativeBehavior = fireEvent.keyDown(box, { key: "Enter" });
    expect(allowedNativeBehavior).toBe(true);
    expect(sendChat).not.toHaveBeenCalled();

    fireEvent.change(box, { target: { value: "line one\nline two" } });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));
    await waitFor(() => expect(sendChat).toHaveBeenCalledTimes(1));
    expect(sendChat).toHaveBeenCalledWith(
      "ada",
      "line one\nline two",
      "pwa-nick",
      expect.any(String)
    );
  });

  it("点「交给 Agent 执行」提交显式任务并跳到任务页", async () => {
    renderTalkChat();
    const box = await screen.findByPlaceholderText("给 ada 发消息…");
    fireEvent.change(box, { target: { value: "帮我整理周报" } });

    fireEvent.click(screen.getByRole("button", { name: "交给 Agent 执行" }));

    await waitFor(() => expect(submitTask).toHaveBeenCalledTimes(1));
    expect(submitTask).toHaveBeenCalledWith("ada", {
      content: "帮我整理周报",
      fromName: "pwa-nick",
      clientMessageId: expect.any(String),
    });
    // 成功后跳到任务页看真实状态（不在聊天页乐观推断）。
    expect(await screen.findByText("任务页占位")).toBeDefined();
  });

  it("把已有消息转为任务：带 source_step_id 保留来源关联", async () => {
    fetchChat.mockResolvedValue(
      chatLog([
        {
          ts: "2026-09-25T10:00:00Z",
          step_id: "s-1",
          from: "pwa-nick",
          to: "ada",
          content: "帮我整理周报",
          reply_to: null,
          filename: null,
          source_url: null,
        },
      ])
    );
    renderTalkChat();

    fireEvent.click(await screen.findByRole("button", { name: "转为任务" }));

    await waitFor(() => expect(submitTask).toHaveBeenCalledTimes(1));
    expect(submitTask).toHaveBeenCalledWith("ada", {
      content: "帮我整理周报",
      fromName: "pwa-nick",
      clientMessageId: expect.any(String),
      sourceStepId: "s-1",
    });
  });
});
