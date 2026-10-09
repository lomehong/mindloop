// 访问凭据控件（手机/PWA 面挂载的 token 录入口）：401 后录入凭据恢复
// 真实查询、挂载前的 401 提示不丢失、取消编辑不落盘。桌面壳/仪表盘
// 2026-10-06 起不再挂载此控件（回环无 token），行为覆盖收在组件层——
// 最小宿主用一条真实 config 查询复现"录入后查询恢复"。

import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { CredentialControl } from "~/components/credential-control";
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
    return Response.json({ version: "dev", git_commit: "", controls_enabled: false });
  }));
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.unstubAllGlobals();
});

/** 最小宿主：一条真实查询（复现"录入后恢复"）+ 控件本身。 */
function Harness() {
  const { data } = useQuery({ queryKey: ["config"], queryFn: fetchConfig, retry: false });
  return (
    <>
      <span data-testid="ver">{data?.version ?? "none"}</span>
      <CredentialControl />
    </>
  );
}

function mount() {
  return render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>
  );
}

it("401 后可在控件录入凭据并恢复真实 GET 查询", async () => {
  mount();
  await waitFor(() => expect(screen.getByText(/请更新访问凭据/)).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "访问凭据" }));
  const input = screen.getByLabelText("Web 访问 Token") as HTMLInputElement;
  expect(input.type).toBe("password");
  expect(input.value).toBe("");
  fireEvent.change(input, { target: { value: "new-test-token" } });
  fireEvent.click(screen.getByRole("button", { name: "保存并重新连接" }));
  await waitFor(() => expect(screen.getByTestId("ver").textContent).toBe("dev"));
  expect(requests.some((request) => request.url === "/api/config" && request.token === "Bearer new-test-token")).toBe(true);
  expect(screen.queryByText(/请更新访问凭据/)).toBeNull();
  expect(screen.queryByLabelText("Web 访问 Token")).toBeNull();
});

it("挂载前的 401 不丢失提示，取消编辑不会改动已保存凭据", async () => {
  await expect(fetchConfig()).rejects.toThrow();
  mount();
  expect(screen.getByText(/请更新访问凭据/)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "访问凭据" }));
  fireEvent.change(screen.getByLabelText("Web 访问 Token"), { target: { value: "not-saved" } });
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(localStorage.getItem("mindloop-web-token")).toBeNull();
  await act(async () => {});
});
