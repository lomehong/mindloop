// 模型档位区行为测试：三档（主/请求/摘要）渲染与生效解析展示、
// 档案+模型选择绑定 providers.json（写面基线 = identity_tiers，
// 全局档位不回写）、env 覆盖警告与身份级清理、未绑定禁选、
// 静态建议兜底与自定义输入。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  Config,
  IdentityEnv,
  LlmConfigView,
  LlmProviderProfileView,
  LlmProvidersView,
} from "~/lib/types";
import { ModelConfigSection } from "~/components/model-config";

const {
  fetchConfig,
  fetchLlmProviders,
  fetchLlmConfig,
  saveLlmProviders,
  deleteEnvVar,
  fetchOpenRouterModels,
} = vi.hoisted(() => ({
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
  fetchLlmConfig: vi.fn(),
  saveLlmProviders: vi.fn(),
  deleteEnvVar: vi.fn(async () => ({ key: "k", value: "", secret: false })),
  fetchOpenRouterModels: vi.fn(async () => ({
    models: [],
    count: 0,
    source: "public" as const,
  })),
}));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchConfig,
    fetchLlmProviders,
    fetchLlmConfig,
    saveLlmProviders,
    deleteEnvVar,
    fetchOpenRouterModels,
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
    paths: { global: "C:\\home\\providers.json", identity: "i" },
    ...overrides,
  };
}

function configView(overrides: Partial<LlmConfigView["tiers"]> = {}): LlmConfigView {
  return {
    tiers: {
      think: { source: "providers.json", model: "glm-4.6", profile: "glm" },
      request: { source: "", model: "", profile: "" },
      summary: { source: "", model: "", profile: "" },
      ...overrides,
    },
  };
}

function envFixture(overrides: Partial<IdentityEnv> = {}): IdentityEnv {
  return {
    identity: { id: "ada", name: "ada" },
    env: [],
    inherited: [],
    note: "",
    ...overrides,
  };
}

function renderSection(env: IdentityEnv = envFixture()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ModelConfigSection identityId="ada" env={env} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  fetchConfig.mockClear();
  fetchLlmProviders.mockClear();
  fetchLlmConfig.mockClear();
  saveLlmProviders.mockClear();
  deleteEnvVar.mockClear();
  fetchOpenRouterModels.mockClear();
  fetchLlmProviders.mockResolvedValue(providersView());
  fetchLlmConfig.mockResolvedValue(configView());
  saveLlmProviders.mockResolvedValue(providersView());
});
afterEach(cleanup);

describe("模型档位区", () => {
  it("渲染三档：档案/模型选择回显绑定，生效解析带来源徽标", async () => {
    renderSection();

    const thinkProfile = (await screen.findByLabelText(
      "主模型 档案选择"
    )) as HTMLSelectElement;
    expect(thinkProfile.value).toBe("glm");
    const thinkModel = screen.getByLabelText("主模型 模型选择") as HTMLSelectElement;
    expect(thinkModel.value).toBe("glm-4.6");
    expect(screen.getByText("providers.json")).toBeDefined();

    // 三档齐全（摘要档补齐）。
    expect(screen.getByLabelText("请求档 档案选择")).toBeDefined();
    expect(screen.getByLabelText("摘要档 档案选择")).toBeDefined();
  });

  it("选择模型即保存：写面带身份级档位，全局合并档位不回写", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        tiers: {
          think: { profile: "glm", model: "glm-4.6" },
          request: { profile: "shared", model: "x-global" },
        },
        identity_tiers: { think: { profile: "glm", model: "glm-4.6" } },
      })
    );
    renderSection();

    const thinkModel = (await screen.findByLabelText(
      "主模型 模型选择"
    )) as HTMLSelectElement;
    fireEvent.change(thinkModel, { target: { value: "glm-4.5-air" } });

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const [identityArg, doc] = saveLlmProviders.mock.calls[0] as [
      string,
      { profiles: { id: string }[]; tiers: Record<string, unknown> },
    ];
    expect(identityArg).toBe("ada");
    expect(doc.profiles.map((p) => p.id)).toEqual(["glm"]);
    expect(doc.tiers).toEqual({
      think: { profile: "glm", model: "glm-4.5-air" },
    });
    expect(doc.tiers).not.toHaveProperty("request");
    await waitFor(() =>
      expect(fetchLlmProviders.mock.calls.length).toBeGreaterThan(1)
    );
  });

  it("换档案后候选 = 新档案模型清单，保存绑定新档案", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        profiles: [
          profileView(),
          profileView({
            id: "local",
            label: "本地网关",
            base_url: "http://127.0.0.1:11434/v1",
            models: ["qwen3"],
          }),
        ],
      })
    );
    renderSection();

    const profileSelect = (await screen.findByLabelText(
      "主模型 档案选择"
    )) as HTMLSelectElement;
    fireEvent.change(profileSelect, { target: { value: "local" } });

    const modelSelect = screen.getByLabelText("主模型 模型选择") as HTMLSelectElement;
    expect(
      within(modelSelect).getByRole("option", { name: "qwen3" })
    ).toBeDefined();
    fireEvent.change(modelSelect, { target: { value: "qwen3" } });

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      tiers: Record<string, unknown>;
    };
    expect(doc.tiers.think).toEqual({ profile: "local", model: "qwen3" });
  });

  it("env 覆盖：警告徽标 + 清理身份级覆盖（继承级不可清）", async () => {
    fetchLlmConfig.mockResolvedValue(
      configView({ think: { source: "env", model: "claude-x", profile: "" } })
    );
    const env = envFixture({
      env: [{ key: "MINDLOOP_MODEL", value: "claude-x", secret: false }],
    });
    renderSection(env);

    expect(await screen.findByText("env 覆盖中")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "清理覆盖 主模型" }));

    await waitFor(() =>
      expect(deleteEnvVar).toHaveBeenCalledWith("ada", "MINDLOOP_MODEL")
    );
  });

  it("清除绑定：移除身份级档位条目（回落到继承/未配置）", async () => {
    renderSection();

    fireEvent.click(
      await screen.findByRole("button", { name: "清除绑定 主模型" })
    );

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      tiers: Record<string, unknown>;
    };
    expect(doc.tiers).toEqual({});
  });

  it("未绑定档案：模型选择禁用；空清单回落静态建议，自定义可保存", async () => {
    fetchLlmProviders.mockResolvedValue(
      providersView({
        profiles: [profileView({ models: [] })],
      })
    );
    renderSection();

    // 空清单：候选回落静态建议（含 echo 占位）。
    const modelSelect = (await screen.findByLabelText(
      "主模型 模型选择"
    )) as HTMLSelectElement;
    expect(
      within(modelSelect).getByRole("option", { name: "echo" })
    ).toBeDefined();

    // 自定义输入保存为新绑定。
    fireEvent.change(modelSelect, { target: { value: "__custom__" } });
    const input = screen.getByPlaceholderText("模型 id，如 glm-4.6");
    fireEvent.change(input, { target: { value: "my-model" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(saveLlmProviders).toHaveBeenCalledTimes(1));
    const doc = saveLlmProviders.mock.calls[0]?.[1] as {
      tiers: Record<string, unknown>;
    };
    expect(doc.tiers.think).toEqual({ profile: "glm", model: "my-model" });

    // 请求档未绑定：模型选择禁用。
    const requestModel = screen.getByLabelText(
      "请求档 模型选择"
    ) as HTMLSelectElement;
    expect(requestModel.disabled).toBe(true);
  });
});
