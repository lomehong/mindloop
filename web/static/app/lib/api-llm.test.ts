// 多提供商配置 API 客户端契约：路径、方法、query（profile/fresh）、
// PUT body 形状（api_key 与 tiers 原样进请求体；服务端负责分流与校验）。
//
// mock 模式与 api-tasks.test.ts 一致：stubGlobal fetch + calls 记录器
// + setWebToken("") 隔离凭据。

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  fetchLlmConfig,
  fetchLlmModels,
  fetchLlmProviders,
  saveLlmProviders,
  setWebToken,
} from "~/lib/api";

interface RecordedCall {
  url: string;
  init?: RequestInit;
}

let calls: RecordedCall[] = [];
let responder: (call: RecordedCall) => unknown;

function jsonResponse(data: unknown): unknown {
  return { ok: true, status: 200, json: async () => data };
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

describe("llm providers 客户端", () => {
  it("fetchLlmProviders GET /llm/providers 并解析有效视图", async () => {
    responder = () =>
      jsonResponse({
        version: 1,
        profiles: [{ id: "glm", origin: "identity", has_key: true }],
        tiers: { think: { profile: "glm", model: "glm-4.6" } },
      });
    const view = await fetchLlmProviders("ada");

    expect(calls[0]?.url).toBe("/api/identities/ada/llm/providers");
    expect(calls[0]?.init?.method).toBeUndefined();
    expect(view.version).toBe(1);
    expect(view.profiles[0]?.id).toBe("glm");
    expect(view.tiers.think?.model).toBe("glm-4.6");
  });

  it("saveLlmProviders PUT 文档：api_key 与 tiers 原样进 body", async () => {
    responder = () => jsonResponse({ version: 1, profiles: [], tiers: {} });
    await saveLlmProviders("ada", {
      version: 1,
      profiles: [{ id: "glm", base_url: "https://a/v1", api_key: "sk-1", models: ["m1"] }],
      tiers: { think: { profile: "glm", model: "glm-4.6" } },
    });

    expect(calls[0]?.url).toBe("/api/identities/ada/llm/providers");
    expect(calls[0]?.init?.method).toBe("PUT");
    expect(bodyOf(calls[0])).toEqual({
      version: 1,
      profiles: [{ id: "glm", base_url: "https://a/v1", api_key: "sk-1", models: ["m1"] }],
      tiers: { think: { profile: "glm", model: "glm-4.6" } },
    });
  });

  it("fetchLlmModels 带 profile 与 fresh query；缺省不带参数", async () => {
    responder = () =>
      jsonResponse({ profile: "glm", models: ["m1"], source: "live" });
    const probe = await fetchLlmModels("ada", "glm", true);

    expect(calls[0]?.url).toBe("/api/identities/ada/llm/models?profile=glm&fresh=1");
    expect(probe.models).toEqual(["m1"]);

    await fetchLlmModels("ada");
    expect(calls[1]?.url).toBe("/api/identities/ada/llm/models");
  });

  it("fetchLlmConfig GET /llm/config 并解析三档", async () => {
    responder = () =>
      jsonResponse({
        tiers: {
          think: { source: "providers.json", model: "glm-4.6", profile: "glm" },
          request: { source: "env", model: "echo", profile: "" },
          summary: { source: "", model: "", profile: "" },
        },
      });
    const view = await fetchLlmConfig("ada");

    expect(calls[0]?.url).toBe("/api/identities/ada/llm/config");
    expect(view.tiers.think?.source).toBe("providers.json");
    expect(view.tiers.request?.source).toBe("env");
    expect(view.tiers.summary?.source).toBe("");
  });
});
