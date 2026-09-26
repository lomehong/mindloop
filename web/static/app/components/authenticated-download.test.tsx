import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BlobLoader } from "~/components/blob-output";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { AuthError, setWebToken } from "~/lib/api";
import { TrajContext } from "~/lib/traj-context";
import Home from "~/routes/home";
import ConfigPage from "~/routes/config";

const fixtures: Record<string, unknown> = {
  "/api/config": { root: "/test", version: "dev", controls_enabled: false, self_update_enabled: false, default_send_from: null, git_commit: null, git_branch: null },
  "/api/identities": [{ id: "ada", name: "ada", path_rel: "ada", created: null, root_trajectory: "root", group: "test", live: false, last_activity_ts: null, step_count: 0, dispatcher: { running: false, pid: null }, thinkers_total: 0, thinkers_active: 0 }],
  "/api/identities/ada/env": { identity: { id: "ada", name: "ada" }, env: [], inherited: [], note: "测试配置" },
  "/api/identities/ada/status": { live: false, pid_alive: false, dispatcher_pid: null, mindlog_mtime: null, mindlog_bytes: 0, step_count: 0 },
  "/api/identities/ada/activity": { state: "asleep", dispatcher_running: false, busy_thinkers: [], last_step_ts: null, last_step_age_s: null, run_seconds: null, stall_after_s: 120, cadence_s: null },
  "/api/openrouter/models": { source: null, has_key: false, count: 0, models: [], error: null, fetched_at: "2026-01-01T00:00:00Z" },
  "/api/identities/ada/export-jobs": [{ job_id: "job-1", identity_id: "ada", status: "done", started_at: "2026-01-01T00:00:00Z", soul_only: false, slim: true, seconds: 1, size: 12, filename: "ada.tgz", error: null, download_url: "https://untrusted.invalid/file" }],
};
let client: QueryClient;
let downloaded: { href: string; filename: string }[];
let files: Blob[];
let requests: string[];
let failDownloads: boolean;
let releaseDownload: (() => void) | undefined;
let holdDownload: boolean;
const blobPath = "/api/identities/ada/traj/root/blob/step.stdout";

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  setWebToken("download-test-token");
  downloaded = [];
  files = [];
  requests = [];
  failDownloads = false;
  holdDownload = false;
  releaseDownload = undefined;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    requests.push(url);
    if (new Headers(init?.headers).get("Authorization") !== "Bearer download-test-token") {
      return Response.json({}, { status: 401 });
    }
    if (url in fixtures) return Response.json(fixtures[url]);
    if (["/api/export", "/api/export-jobs/job-1/download", blobPath].includes(url)) {
      if (holdDownload) await new Promise<void>((resolve) => { releaseDownload = resolve; });
      if (failDownloads) return Response.json({}, { status: 401 });
      return new Response("完整测试输出", { headers: { "Content-Type": "text/plain" } });
    }
    throw new Error(`未预期的测试请求：${url}`);
  }));
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: (blob: Blob) => { files.push(blob); return "blob:authenticated-file"; } });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    downloaded.push({ href: this.href, filename: this.download });
  });
});
afterEach(() => {
  releaseDownload?.();
  cleanup();
  client.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function mount(node: ReactNode, path = "/") {
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}>
    <Routes><Route path={path === "/" ? "/" : "/i/:identityId/config"} element={node} /></Routes>
  </MemoryRouter></QueryClientProvider>);
}
function mountBlob() {
  mount(<TrajContext.Provider value={{ identityId: "ada", trajId: "root" }}>
    <BlobLoader blobRef="blobs/step.stdout" totalBytes={10000} />
  </TrajContext.Provider>);
}

it("首页导出全部通过认证获取文件，只下载对象 URL", async () => {
  mount(<Home />);
  fireEvent.click(await screen.findByText("导出全部"));
  await waitFor(() => expect(downloaded).toHaveLength(1));
  expect(downloaded[0]).toEqual({ href: "blob:authenticated-file", filename: "mindloop-identities.tgz" });
  expect(await files[0].text()).toBe("完整测试输出");
  expect(requests.filter((url) => url === "/api/export")).toHaveLength(1);
});

it("导出构建下载按任务 ID 请求，不信任服务返回的外部下载地址", async () => {
  mount(<ConfigPage />, "/i/ada/config");
  fireEvent.click(await screen.findByText(/ada\.tgz/));
  await waitFor(() => expect(downloaded).toHaveLength(1));
  expect(downloaded[0]).toEqual({ href: "blob:authenticated-file", filename: "ada.tgz" });
  expect(requests).toContain("/api/export-jobs/job-1/download");
});

it("下载进行中禁用重复点击，401 后显示凭据提示并允许再次操作", async () => {
  holdDownload = true;
  failDownloads = true;
  mount(<Home />);
  fireEvent.click(await screen.findByText("导出全部"));
  await waitFor(() => expect(releaseDownload).toBeDefined());
  const button = screen.getByRole("button", { name: /下载中/ }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.click(button);
  releaseDownload?.();
  await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/访问凭据/));
  expect(requests.filter((url) => url === "/api/export")).toHaveLength(1);
  expect(downloaded).toHaveLength(0);
  expect((screen.getByRole("button", { name: "导出全部" }) as HTMLButtonElement).disabled).toBe(false);
});

it("完整输出读取及原始文件下载都携带凭据", async () => {
  mountBlob();
  fireEvent.click(screen.getByRole("button", { name: "load full output" }));
  expect(await screen.findByText("完整测试输出")).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "下载原始输出" }));
  await waitFor(() => expect(downloaded).toHaveLength(1));
  expect(downloaded[0]).toEqual({ href: "blob:authenticated-file", filename: "step.stdout" });
  expect(requests.filter((url) => url === blobPath)).toHaveLength(2);
});

it("完整输出的 401 使用统一提示且不显示未授权内容", async () => {
  failDownloads = true;
  mountBlob();
  fireEvent.click(screen.getByRole("button", { name: "load full output" }));
  expect(await screen.findByText(/访问凭据缺失或已失效/)).toBeDefined();
  expect(screen.queryByText("完整测试输出")).toBeNull();
});

it("认证失败的页面引导更新凭据，不误报服务未运行", () => {
  mount(<QueryErrorBanner error={new AuthError()} onRetry={() => {}} />);
  expect(screen.queryByText(/服务是否在运行/)).toBeNull();
  expect(screen.getByRole("alert").textContent).toMatch(/访问凭据/);
});
