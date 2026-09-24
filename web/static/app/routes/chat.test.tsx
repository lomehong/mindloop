// Composer keyboard behavior on the chat page: Enter sends, Shift+Enter
// does not (so it can insert a newline via native textarea behavior).

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ChatPage from "~/routes/chat";
import type {
  ChatLog,
  Config,
  IdentityActivity,
  IdentityStatus,
  ThinkersStatus,
} from "~/lib/types";

// jsdom ships no scrollIntoView; the message list auto-scrolls with one.
Element.prototype.scrollIntoView ??= () => {};

const sendChat = vi.fn(
  async (_identityId: string, _content: string, from: string) => ({
    ok: true,
    from,
    to: "ada",
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
    fetchIdentityStatus: vi.fn(
      async (): Promise<IdentityStatus> => ({
        live: false,
        pid_alive: false,
        dispatcher_pid: null,
        mindlog_mtime: null,
        mindlog_bytes: null,
        step_count: 0,
      })
    ),
    fetchThinkers: vi.fn(
      async (): Promise<ThinkersStatus> => ({
        identity: { id: "ada", name: "ada" },
        dispatcher: { running: true, pid: 1 },
        active_thinkers: 0,
        thinkers_total: 0,
        thinkers_disabled: 0,
        steps_in_flight: 0,
        pending_total: 0,
        thinkers: [],
      })
    ),
    fetchActivity: vi.fn(
      async (): Promise<IdentityActivity> => ({
        state: "idle",
        dispatcher_running: true,
        steps_in_flight: 0,
        pending_total: 0,
        busy_thinkers: [],
        last_step_ts: null,
        last_step_age_s: null,
        run_seconds: null,
        stall_after_s: 60,
        cadence_s: null,
        queued_messages: [],
      })
    ),
    fetchChat: vi.fn(
      async (): Promise<ChatLog> => ({
        identity: { id: "ada", name: "ada" },
        live: false,
        messages: [],
        outcomes: {},
      })
    ),
    sendChat: (...args: Parameters<typeof sendChat>) => sendChat(...args),
  };
});

function renderChatPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/i/ada/chat"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/i/:identityId/chat" element={<ChatPage />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  sendChat.mockClear();
});
afterEach(cleanup);

describe("chat composer", () => {
  it("Enter sends the draft and clears the box", async () => {
    renderChatPage();
    const box = await screen.findByPlaceholderText("Message ada…");
    fireEvent.change(box, { target: { value: "hello there" } });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(sendChat).toHaveBeenCalledTimes(1));
    expect(sendChat).toHaveBeenCalledWith("ada", "hello there", "you");
    await waitFor(() =>
      expect((box as HTMLTextAreaElement).value).toBe("")
    );
  });

  it("Shift+Enter does not send", async () => {
    renderChatPage();
    const box = await screen.findByPlaceholderText("Message ada…");
    fireEvent.change(box, { target: { value: "line one" } });
    fireEvent.keyDown(box, { key: "Enter", shiftKey: true });
    // give any (incorrect) submit a tick to fire before asserting it didn't
    await new Promise((r) => setTimeout(r, 0));
    expect(sendChat).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe("line one");
  });

  it("Enter does not send while an input method composition is active", async () => {
    renderChatPage();
    const box = await screen.findByPlaceholderText("Message ada…");
    fireEvent.change(box, { target: { value: "unfinished" } });
    fireEvent.keyDown(box, { key: "Enter", isComposing: true });
    await new Promise((r) => setTimeout(r, 0));
    expect(sendChat).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe("unfinished");
  });
});
