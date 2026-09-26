import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Navbar } from "~/components/navbar";
import { fetchConfig, setWebToken } from "~/lib/api";

let requests: { url: string; token: string | null }[];
let client: QueryClient;
beforeEach(() => {
  setWebToken("");
  requests = [];
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const token = new Headers(init?.headers).get("Authorization");
    requests.push({ url, token });
    if (token !== "Bearer new-test-token") return Response.json({}, { status: 401 });
    if (url.endsWith("llm-health")) return Response.json({ status: "unknown", identities: [] });
    return Response.json({ version: "dev", git_commit: "", controls_enabled: false });
  }));
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.unstubAllGlobals();
});

function mountNavbar() {
  return render(<QueryClientProvider client={client}><MemoryRouter><Navbar /></MemoryRouter></QueryClientProvider>);
}

it("401 后可在导航栏更新凭据并恢复真实 GET 查询", async () => {
  mountNavbar();
  await waitFor(() => expect(screen.getByText(/请更新访问凭据/)).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "访问凭据" }));
  const input = screen.getByLabelText("Web 访问 Token") as HTMLInputElement;
  expect(input.type).toBe("password");
  expect(input.value).toBe("");
  fireEvent.change(input, { target: { value: "new-test-token" } });
  fireEvent.click(screen.getByRole("button", { name: "保存并重新连接" }));
  await waitFor(() => expect(client.getQueryData(["config"])).toMatchObject({ version: "dev" }));
  expect(requests.some((request) => request.url === "/api/config" && request.token === "Bearer new-test-token")).toBe(true);
  expect(screen.queryByText(/请更新访问凭据/)).toBeNull();
  expect(screen.queryByLabelText("Web 访问 Token")).toBeNull();
});

it("挂载前的 401 不丢失提示，取消编辑不会改动已保存凭据", async () => {
  await expect(fetchConfig()).rejects.toThrow();
  mountNavbar();
  expect(screen.getByText(/请更新访问凭据/)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "访问凭据" }));
  fireEvent.change(screen.getByLabelText("Web 访问 Token"), { target: { value: "not-saved" } });
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(localStorage.getItem("mindloop-web-token")).toBeNull();
  await act(async () => {});
});
