import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let api: typeof import("~/lib/api");
let requests: { url: string; init?: RequestInit }[];
let respond: () => Response;

beforeEach(async () => {
  vi.resetModules();
  localStorage.clear();
  window.history.replaceState({ test: true }, "", "/");
  requests = [];
  respond = () => Response.json({ ok: true });
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    requests.push({ url, init });
    return respond();
  }));
  api = await import("~/lib/api");
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("统一 Web 凭据", () => {
  it("GET、JSON 写入、上传和 SSE 都携带相同的本地 Token", async () => {
    localStorage.setItem("mindloop-web-token", "test-token");
    await api.fetchConfig();
    await api.sendChat("ada", "你好", "operator");
    await api.importIdentities(new File(["archive"], "test.tgz"));
    respond = () => new Response("event: done\ndata: {\"reply_to\":\"m1\"}\n\n");
    const events: unknown[] = [];
    await api.openReplyStream("ada", (event) => events.push(event));
    expect(requests).toHaveLength(4);
    for (const request of requests) {
      expect(new Headers(request.init?.headers).get("Authorization")).toBe("Bearer test-token");
      expect(request.url).not.toContain("test-token");
    }
    expect(new Headers(requests[1].init?.headers).get("Content-Type")).toBe("application/json");
    expect(new Headers(requests[2].init?.headers).get("Content-Type")).toBe("application/gzip");
    expect(events).toEqual([{ type: "done", reply_to: "m1" }]);
  });

  it("读取 URL Token 后清除敏感参数，保留路由、其他查询和 history state", async () => {
    localStorage.setItem("mindloop-web-token", "old");
    window.history.replaceState({ test: true }, "", "/talk/ada?token=url-test&tail=50#latest");
    await api.fetchConfig();
    expect(new Headers(requests[0].init?.headers).get("Authorization")).toBe("Bearer url-test");
    expect(window.location.pathname + window.location.search + window.location.hash).toBe("/talk/ada?tail=50#latest");
    expect(window.history.state).toEqual({ test: true });
    expect(localStorage.getItem("mindloop-web-token")).toBe("url-test");
  });

  it("本地存储不可用时本次会话仍使用 URL Token 并清理地址栏", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    window.history.replaceState(null, "", "/?token=session-test");
    await api.fetchConfig();
    await api.fetchConfig();
    expect(window.location.search).toBe("");
    for (const request of requests) {
      expect(new Headers(request.init?.headers).get("Authorization")).toBe("Bearer session-test");
    }
  });

  it.each(["get", "write", "upload", "sse"])("%s 的 401 给出统一更新凭据提示", async (kind) => {
    respond = () => Response.json({ detail: { message: "Unauthorized" } }, { status: 401 });
    const request = kind === "get" ? api.fetchConfig()
      : kind === "write" ? api.sendChat("ada", "消息", "operator")
      : kind === "upload" ? api.importIdentities(new File(["archive"], "test.tgz"))
      : api.openReplyStream("ada", () => {});
    await expect(request).rejects.toThrow("请更新访问凭据");
    expect(requests).toHaveLength(1);
  });

  it("更新凭据后后续请求使用新值，撤销凭据清除缓存，不重放写操作", async () => {
    localStorage.setItem("mindloop-web-token", "old-test");
    respond = () => Response.json({}, { status: 401 });
    await expect(api.sendChat("ada", "消息", "operator")).rejects.toThrow();
    expect(api.authRequired()).toBe(true);
    api.setWebToken("new-test");
    expect(api.authRequired()).toBe(false);
    respond = () => Response.json({ ok: true });
    await api.fetchConfig();
    expect(new Headers(requests[1].init?.headers).get("Authorization")).toBe("Bearer new-test");
    api.setWebToken("");
    await api.fetchConfig();
    expect(new Headers(requests[2].init?.headers).has("Authorization")).toBe(false);
    expect(localStorage.getItem("mindloop-web-token")).toBeNull();
    expect(requests.filter((request) => request.init?.method === "POST")).toHaveLength(1);
  });

  it("未配置 Token 不发送空 Authorization，非 401 保留服务端错误", async () => {
    await api.fetchConfig();
    expect(new Headers(requests[0].init?.headers).has("Authorization")).toBe(false);
    respond = () => Response.json({ detail: { message: "身份不存在" } }, { status: 404 });
    await expect(api.fetchConfig()).rejects.toThrow("身份不存在");
  });

  it("认证下载只将短期 Blob URL 交给浏览器，并释放对象 URL", async () => {
    vi.useFakeTimers();
    localStorage.setItem("mindloop-web-token", "download-test");
    respond = () => new Response("archive-data");
    const create = vi.fn<(blob: Blob) => string>(() => "blob:test-download");
    const revoke = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: create });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoke });
    const clicks: { href: string; filename: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicks.push({ href: this.href, filename: this.download });
    });
    await api.downloadFile(api.exportAllUrl(), "identities.tgz");
    expect(requests[0].url).toBe("/api/export");
    expect(new Headers(requests[0].init?.headers).get("Authorization")).toBe("Bearer download-test");
    expect(create.mock.calls[0][0].size).toBe(12);
    expect(clicks).toEqual([{ href: "blob:test-download", filename: "identities.tgz" }]);
    expect(document.querySelector('a[download]')).toBeNull();
    await vi.runAllTimersAsync();
    expect(revoke).toHaveBeenCalledWith("blob:test-download");
  });

  it("下载失败或外部地址不能生成 Blob URL 或泄露凭据", async () => {
    respond = () => Response.json({}, { status: 401 });
    await expect(api.downloadFile(api.exportAllUrl(), "all.tgz")).rejects.toThrow("请更新访问凭据");
    requests = [];
    await expect(api.downloadFile("https://outside.invalid/archive", "all.tgz")).rejects.toThrow();
    expect(requests).toHaveLength(0);
  });
});
