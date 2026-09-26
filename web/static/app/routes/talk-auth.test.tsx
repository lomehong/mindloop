import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setWebToken } from "~/lib/api";
import { setPwaName } from "~/lib/pwa";
import TalkHome from "~/routes/talk";
import TalkChat from "~/routes/talk-chat";

const fixtures: Record<string, unknown> = {
  "/api/config": { root: "/test", version: "dev", controls_enabled: true, self_update_enabled: false, default_send_from: null, git_commit: null, git_branch: null },
  "/api/identities": [{ id: "ada", name: "ada", path_rel: "ada", created: null, root_trajectory: "root", group: "test", live: false, last_activity_ts: null, step_count: 0, dispatcher: { running: false, pid: null }, thinkers_total: 0, thinkers_active: 0 }],
  "/api/identities/ada/chat": { identity: { id: "ada", name: "ada" }, live: false, messages: [], outcomes: {} },
  "/api/identities/ada/thinkers": { identity: { id: "ada", name: "ada" }, dispatcher: { running: false, pid: null }, active_thinkers: 0, thinkers_total: 0, thinkers_disabled: 0, thinkers: [] },
};
let client: QueryClient;
let requests: { path: string; method: string }[];

beforeEach(() => {
  localStorage.clear();
  setWebToken("");
  requests = [];
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const path = new URL(url, "http://localhost").pathname;
    requests.push({ path, method: init?.method ?? "GET" });
    if (new Headers(init?.headers).get("Authorization") !== "Bearer mobile-test-token") {
      return Response.json({}, { status: 401 });
    }
    if (path.endsWith("/replies/stream")) {
      return new Response(new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('event: status\ndata: {"replying":false,"reply_to":""}\n\n'));
          init?.signal?.addEventListener("abort", () => controller.close(), { once: true });
        },
      }));
    }
    if (!(path in fixtures)) throw new Error(`未预期的请求 ${path}`);
    return Response.json(fixtures[path]);
  });
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.unstubAllGlobals();
});

function mount(path: string) {
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}>
    <Routes>
      <Route path="/talk" element={<TalkHome />} />
      <Route path="/talk/:identityId" element={<TalkChat />} />
    </Routes>
  </MemoryRouter></QueryClientProvider>);
}
function saveCredentials(button: HTMLElement) {
  fireEvent.click(button);
  fireEvent.change(screen.getByLabelText("Web 访问 Token"), { target: { value: "mobile-test-token" } });
  fireEvent.click(screen.getByRole("button", { name: "保存并重新连接" }));
}

it("未填写名字也能设置凭据，随后查询身份使用新凭据", async () => {
  mount("/talk");
  saveCredentials(screen.getByRole("button", { name: "访问凭据" }));
  expect(requests).toHaveLength(0);
  fireEvent.change(screen.getByPlaceholderText("你的名字"), { target: { value: "nick" } });
  fireEvent.click(screen.getByRole("button", { name: "开始" }));
  expect(await screen.findByText("ada")).toBeDefined();
  expect(requests.every((request) => request.method === "GET")).toBe(true);
});

it("身份选择页的认证失败不会显示成没有身份，更新后恢复列表", async () => {
  setPwaName("nick");
  mount("/talk?pick=1");
  expect(await screen.findByText(/访问凭据缺失或已失效/)).toBeDefined();
  expect(screen.queryByText("这台服务器上还没有身份。")).toBeNull();
  saveCredentials(screen.getByRole("button", { name: "访问凭据" }));
  expect(await screen.findByText("ada")).toBeDefined();
});

it("聊天视口内的页头可更新凭据，恢复输入框但不自动发送消息", async () => {
  setPwaName("nick");
  mount("/talk/ada");
  await screen.findByText("请更新访问凭据");
  saveCredentials(within(screen.getByRole("banner")).getByRole("button", { name: "访问凭据" }));
  expect(await screen.findByPlaceholderText("给 ada 发消息…")).toBeDefined();
  expect(requests.every((request) => request.method === "GET")).toBe(true);
  expect(screen.queryByLabelText("Web 访问 Token")).toBeNull();
});
