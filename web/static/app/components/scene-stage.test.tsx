// SceneStage 舞台分区制渲染测试（theater.md §2/§7/§16.3）：confront
// 遮罩独占、右列至多 3 张整卡 + 溢出折叠 chip、心跳行读数、审批卡
// 依据行（按 hash 前缀匹配真实审批载荷）与记住偏好快捷键。宇宙层
// 在 jsdom 无 WebGL 自动走降级路径，不参与断言。

import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { SceneStage } from "~/components/scene-layer";
import type { SceneSurface } from "~/lib/scene";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  vi.restoreAllMocks();
});

const NOW = Date.now();

function approvalFixture(): Record<string, unknown> {
  return {
    hash: "abcd1234" + "0".repeat(56),
    script: "git push origin main\n# 注释",
    work_dir: "/home/ada/proj",
    run_id: "run-1",
    created: new Date(NOW - 22_000).toISOString(),
    expires: new Date(NOW + 30 * 60_000).toISOString(),
    risks: ["外发代码"],
  };
}

function stubFetch(opts: { approvals?: unknown[] } = {}) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const json = (body: unknown) =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    if (url.includes("/activity")) {
      return json({
        state: "working",
        dispatcher_running: true,
        busy_thinkers: ["monolith"],
        last_step_ts: new Date(NOW - 5000).toISOString(),
        last_step_age_s: 5,
        run_seconds: 1380,
        stall_after_s: 900,
        cadence_s: null,
      });
    }
    if (url.includes("/thinkers")) return json({ thinkers: [] });
    if (url.includes("/status")) {
      return json({ live: true, step_count: 431 });
    }
    if (url.includes("/threads")) {
      return json([]);
    }
    if (url.includes("/approvals")) {
      return json(opts.approvals ?? []);
    }
    if (url.includes("/usage")) {
      return json({
        admission: { daily_limit: 2000, used_today: 1500, consecutive_errors: 0, circuit_threshold: 8 },
      });
    }
    if (url.includes("/tasks")) return json([]);
    if (url.includes("/chat")) {
      return json({ identity: { id: "ada", name: "ada" }, messages: [] });
    }
    return json({});
  });
}

function choiceSurface(): SceneSurface {
  return {
    id: "approve-abcd1234",
    kind: "choice",
    intent: "confront",
    focus: true,
    summary: "git push origin main",
    data: {
      prompt: "允许执行以下脚本？",
      options: [
        { value: "approve", label: "允许", tone: "primary" },
        { value: "deny", label: "拒绝", tone: "danger" },
      ],
      context: { script: "git push origin main\n# 注释", work_dir: "/home/ada/proj" },
    },
  };
}

function textSurface(id: string, title: string): SceneSurface {
  return { id, kind: "text", intent: "inform", summary: `${title} 正文`, data: { title, body: `${title} 正文` } };
}

function renderStage(surfaces: SceneSurface[], approvals: unknown[] = []) {
  vi.stubGlobal("fetch", stubFetch({ approvals }));
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SceneStage
          surfaces={surfaces}
          identityId="ada"
          pulse={null}
          leaving={[]}
          sedimented={[]}
          onExpand={() => {}}
        />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("SceneStage 舞台分区制", () => {
  beforeAll(() => {
    // jsdom 无 WebGL：显式声明缺失，宇宙层走 CSS 降级（真实降级路径）。
    (window as unknown as { WebGLRenderingContext?: unknown }).WebGLRenderingContext = undefined;
  });

  it("confront 遮罩居中，其余卡至多 3 张整卡 + 溢出折叠 chip", async () => {
    renderStage(
      [
        choiceSurface(),
        textSurface("t1", "卡片一"),
        textSurface("t2", "卡片二"),
        textSurface("t3", "卡片三"),
        textSurface("t4", "卡片四"),
      ],
      [approvalFixture()]
    );
    // confront 卡在遮罩里
    await waitFor(() => {
      expect(screen.getByText("允许执行以下脚本？")).toBeDefined();
    });
    // 三张整卡可见
    expect(screen.getByText("卡片一")).toBeDefined();
    expect(screen.getByText("卡片二")).toBeDefined();
    expect(screen.getByText("卡片三")).toBeDefined();
    // 第四张被折叠：标题不可见，但溢出 chip 计数可见
    expect(screen.queryByText("卡片四")).toBeNull();
    expect(screen.getByTestId("scene-overflow").textContent).toContain("还有 1 项");
  });

  it("心跳行 awaiting 分支：已等待时长来自最旧审批的 created", async () => {
    renderStage([choiceSurface()], [approvalFixture()]);
    const hb = await screen.findByTestId("work-heartbeat");
    await waitFor(() => {
      expect(hb.textContent).toContain("等你拍板");
      expect(hb.textContent).toMatch(/已等 \d+s/);
    });
  });

  it("审批卡依据行：risks 来自真实审批载荷，按 hash 前缀匹配", async () => {
    renderStage([choiceSurface()], [approvalFixture()]);
    await waitFor(() => {
      expect(screen.getByText(/依据：/).textContent).toContain("外发代码");
    });
  });

  it("记住偏好：localStorage 有同命令族决定时给「按上次决定」", async () => {
    window.localStorage.setItem(
      "theater-decision-ada",
      JSON.stringify({ "git push origin main": "approve" })
    );
    renderStage([choiceSurface()], [approvalFixture()]);
    await waitFor(() => {
      expect(screen.getByTestId("scene-select-repeat").textContent).toContain("按上次决定（允许）");
    });
  });

  it("无同命令族记录时不渲染记住偏好按钮", async () => {
    renderStage([choiceSurface()], [approvalFixture()]);
    await screen.findByText("允许执行以下脚本？");
    expect(screen.queryByTestId("scene-select-repeat")).toBeNull();
  });
});
