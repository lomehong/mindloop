// 提供商档案区行为测试：渲染（密钥状态与归属徽标）、编辑保存
// （api_key 分流进请求体、tiers 原样保留）、添加、拉取模型导入、
// 删除联动清理档位绑定、全局级档案只读、坏文档错误展示。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  Config,
  LlmModelsProbe,
  LlmProviderProfileView,
  LlmProvidersView,
} from "~/lib/types";
import { ProviderProfilesSection } from "~/components/provider-profiles";

const { fetchConfig, fetchLlmProviders, saveLlmProviders, fetchLlmModels } =
  vi.hoisted(() => ({
    fetchConfig: vi.fn(
      async (): Promise<Config> => ({
        root: "/home/.mindloop/identities",
        version: "test",
        controls_enabled: true,
        self_update_enabled: false,
        default_send_from: null,
        git_commit: null,
        git_branch: null,
      })
    ),
    fetchLlmProviders: vi.fn(),
    saveLlmProviders: vi.fn(),
    fetchLlmModels: vi.fn(),
  }));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchConfig,
    fetchLlmProviders,
    saveLlmProviders,
    fetchLlmModels,
  };
});

function profileView(
  overrides: Partial<LlmProviderProfileView> = {}
): LlmProviderProfileView {
  return {
    id: "glm",
    label: "智谱",
    provider: "openai-compatible",
    base_url: "https://open.bigmodel.cn/api/paas/v4",
    api_key_env: "",
    models: ["glm-4.6", "glm-4.5-air"],
    key_env_name: "MINDLOOP_PROFILE_GLM_API_KEY",
    has_key: true,
    key_source: "identity",
    origin: "identity",
    ...overrides,
  };
}

function providersView(overrides: Partial<LlmProvidersView> = {}): LlmProvidersView {
  return {
    identity: { id: "ada", name: "ada" },
    version: 1,
    profiles: [profileView()],
    tiers: { think: { profile: "glm", model: "glm-4.6" } },
    identity_tiers: { think: { profile: "glm", model: "glm-4.6" } },
    paths: {
      global: "C:\\home\\providers.json",
      identity: "C:\\home\\identities\\ada\\providers.json",
    },
    ...overrides,
  };
}

function renderSection(identityId = "ada") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ProviderProfilesSection identityId={identityId} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  fetchConfig.mockClear();
  fetchLlmProviders.mockClear();
  saveLlmProviders.mockClear();
  fetchLlmModels.mockClear();
  fetchLlmProviders.mockResolvedValue(providersView());
  saveLlmProviders.mockResolvedValue(providersView());
  fetchLlmModels.mockResolvedValue({
    profile: "glm",
    models: ["m-a", "m-b"],
    source: "live",
  } satisfies LlmModelsProbe);
});
afterEach(cleanup);

describe("提供商档案区", () => {
  it("渲染档案：连接、密钥状态（身份/环境/未配置）与归属徽标", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        profiles: [
          profileView(),
          profileView({
            id: "local",
            label: "本地网关",
            base_url: "http://127.0.0.1:11434/v1",
            has_key: false,
            key_env_name: "LOCAL_KEY",
            key_source: "",
            origin: "global",
          }),
        ],
      })
    );
    renderSection();

    expect(await screen.findByText("智谱")).toBeDefined();
    expect(
      screen.getByText("https://open.bigmodel.cn/api/paas/v4")
    ).toBeDefined();
    expect(screen.getByText("密钥已配置（身份 .env）")).toBeDefined();
    expect(screen.getByText("未配置 · LOCAL_KEY")).toBeDefined();
    expect(screen.getByText("身份级")).toBeDefined();
    expect(screen.getByText("全局级")).toBeDefined();
  });

  it("编辑保存：api_key 分流进 profile.api_key，tiers 原样保留，成功后重新取数", async () => {
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "编辑 glm" }));

    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://new.example/v1" },
    });
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "sk-new" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const [identityArg, doc] = saveLlmProviders.mock.calls[0] as [
      string,
      {
        profiles: { id: string; base_url: string; api_key?: string; models?: string[] }[];
        tiers: Record<string, unknown>;
      },
    ];
    expect(identityArg).toBe("ada");
    expect(doc.profiles).toHaveLength(1);
    expect(doc.profiles[0]).toMatchObject({
      id: "glm",
      base_url: "https://new.example/v1",
      api_key: "sk-new",
      models: ["glm-4.6", "glm-4.5-air"],
    });
    expect(doc.tiers).toEqual({ think: { profile: "glm", model: "glm-4.6" } });
    await waitFor(() =>
      expect(fetchLlmProviders.mock.calls.length).toBeGreaterThan(1)
    );
  });

  it("添加档案：新档案与既有档案、tiers 一并进保存文档", async () => {
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "添加档案" }));

    fireEvent.change(screen.getByLabelText("ID"), { target: { value: "local" } });
    fireEvent.change(screen.getByLabelText("名称"), {
      target: { value: "本地网关" },
    });
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "http://127.0.0.1:11434/v1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      profiles: { id: string; label?: string }[];
      tiers: Record<string, unknown>;
    };
    expect(doc.profiles.map((p) => p.id)).toEqual(["glm", "local"]);
    expect(doc.profiles[1]?.label).toBe("本地网关");
    expect(doc.tiers).toEqual({ think: { profile: "glm", model: "glm-4.6" } });
  });

  it("从提供商拉取：调用 fresh 探测并填入模型清单；失败显示错误", async () => {
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "编辑 glm" }));
    fireEvent.click(screen.getByRole("button", { name: "从提供商拉取" }));

    await waitFor(() =>
      expect(fetchLlmModels).toHaveBeenCalledWith("ada", "glm", true)
    );
    const textarea = screen.getByLabelText("模型清单（每行一个）") as HTMLTextAreaElement;
    await waitFor(() => expect(textarea.value).toBe("m-a\nm-b"));

    // 探测失败（错误装 error 字段）：内联报错，不覆盖表单内容。
    fetchLlmModels.mockResolvedValue({
      profile: "glm",
      models: [],
      source: "",
      error: "llm: HTTP 401: bad key",
    } satisfies LlmModelsProbe);
    fireEvent.click(screen.getByRole("button", { name: "从提供商拉取" }));
    expect(await screen.findByRole("alert")).toBeDefined();
    expect(screen.getByText(/llm: HTTP 401/)).toBeDefined();
    expect(textarea.value).toBe("m-a\nm-b");
  });

  it("保存只带身份级档位：合并视图里的全局档位不回写", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        // 读面合并：think 来自身份、request 来自全局；写面基线只有 think。
        tiers: {
          think: { profile: "glm", model: "glm-4.6" },
          request: { profile: "shared", model: "x-global" },
        },
        identity_tiers: { think: { profile: "glm", model: "glm-4.6" } },
      })
    );
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "编辑 glm" }));
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      tiers: Record<string, unknown>;
    };
    expect(doc.tiers).toEqual({ think: { profile: "glm", model: "glm-4.6" } });
    expect(doc.tiers).not.toHaveProperty("request");
  });

  it("删除：确认后文档移除该档案，并清理引用它的档位绑定", async () => {
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "删除 glm" }));
    fireEvent.click(await screen.findByRole("button", { name: "删除" }));

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      profiles: unknown[];
      tiers: Record<string, unknown>;
    };
    expect(doc.profiles).toEqual([]);
    expect(doc.tiers).toEqual({});
  });

  it("全局级档案只读：无编辑/删除入口", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        profiles: [profileView({ id: "shared", label: "共享", origin: "global" })],
      })
    );
    renderSection();

    expect(await screen.findByText("共享")).toBeDefined();
    expect(screen.getByText("全局级")).toBeDefined();
    expect(screen.queryByRole("button", { name: "编辑 shared" })).toBeNull();
    expect(screen.queryByRole("button", { name: "删除 shared" })).toBeNull();
  });

  it("坏文档：展示错误原文（可用 PUT 覆盖修复，页面不崩）", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        profiles: [],
        error: "config: 解析 C:\\home\\identities\\ada\\providers.json: unexpected EOF",
      })
    );
    renderSection();

    expect(await screen.findByText(/unexpected EOF/)).toBeDefined();
    expect(screen.getByRole("button", { name: "添加档案" })).toBeDefined();
  });
});
