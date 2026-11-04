import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";

import { StepContent } from "~/components/step-card";
import { TrajContext } from "~/lib/traj-context";
import type { NormalizedStep } from "~/lib/types";

globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

/** ForkExpand 内嵌 useQuery/TrajContext/Link——测试统一包 provider。 */
function renderWithProviders(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TrajContext.Provider value={{ identityId: "ada", trajId: "root-traj" }}>
          {ui}
        </TrajContext.Provider>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("Slack source links", () => {
  it("uses the exact inherited permalink on a reply step", () => {
    const sourceUrl =
      "https://laudesters.slack.com/archives/C123/p1788451200123456";
    const step: NormalizedStep = {
      step_id: "m2",
      ts: "2026-09-03T12:00:01Z",
      type: "message",
      source: "chat",
      preview: "done",
      raw: {
        from: "audel",
        to: "slack-U1-C123-1788451200.123456",
        content: "done",
        reply_to: "m1",
      },
      run_id: null,
      source_url: sourceUrl,
    };

    render(<StepContent step={step} expandAll />);

    const link = screen.getByRole("link", { name: /在 Slack 中打开/ });
    expect(link.getAttribute("href")).toBe(sourceUrl);
  });
});

describe("Super-agent 控制面步骤渲染（fork 穿透）", () => {
  const mk = (type: NormalizedStep["type"], raw: Record<string, unknown>, extra: Partial<NormalizedStep> = {}): NormalizedStep => ({
    step_id: "s1",
    ts: "2026-11-05T00:00:00Z",
    type,
    source: null,
    preview: "",
    raw,
    run_id: null,
    ...extra,
  });

  it("mandate 渲染授权三要素 chip", () => {
    renderWithProviders(
      <StepContent
        step={mk("mandate", {
          task_id: "0123456789abcdef",
          from: "operator",
          exec_policy: "auto(缺省)",
        })}
        expandAll={false}
      />
    );
    expect(screen.getByText(/已授权执行/)).toBeTruthy();
    expect(screen.getByText(/task 01234567/)).toBeTruthy();
    expect(screen.getByText(/from operator/)).toBeTruthy();
    expect(screen.getByText(/policy auto/)).toBeTruthy();
  });

  it("merge 渲染 Worker 汇报链接与正文", () => {
    renderWithProviders(
      <StepContent
        step={mk(
          "merge",
          { from_traj: "ffffffff-0000-0000-0000-000000000000", content: "worker 完成（fork 轨迹 abc）" },
          { writeback: { from_traj: "ffffffff-0000-0000-0000-000000000000", from_step: null } }
        )}
        expandAll={false}
      />
    );
    const link = screen.getByRole("link", { name: /Worker 汇报/ });
    expect(link.getAttribute("href")).toContain("/t/ffffffff");
    expect(screen.getByText(/worker 完成（fork 轨迹 abc）/)).toBeTruthy();
  });

  it("fork 带内联展开入口与子轨迹链接", () => {
    renderWithProviders(
      <StepContent
        step={mk(
          "fork",
          { child: "aaaaaaaa-0000-0000-0000-000000000000", child_ref: "../x" },
          { fork: { child_traj_id: "aaaaaaaa-0000-0000-0000-000000000000", slug: "worker-123", resolved: true } }
        )}
        expandAll={false}
      />
    );
    const link = screen.getByRole("link", { name: /worker-123/ });
    expect(link.getAttribute("href")).toContain("/t/aaaaaaaa");
    // 展开入口存在（折叠态，不触发请求）。
    expect(screen.getByRole("button", { name: /Worker 执行详情/ })).toBeTruthy();
  });
});
