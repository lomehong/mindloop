// 用量页的预算与准入展示测试：admission 块（每日预算/熔断）与
// unknown_calls（供应商未返回用量的调用）在页面上如实呈现——预算
// 未设置显示"未设置"，冷却中显示截止时刻，未知用量不能被静默吞掉。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Usage } from "~/lib/types";
import UsagePage from "~/routes/usage";

const { fetchUsage, fetchIdentityStatus, fetchConfig } = vi.hoisted(() => ({
  fetchUsage: vi.fn(),
  fetchIdentityStatus: vi.fn(async () => ({
    live: false,
    pid_alive: false,
    dispatcher_pid: null,
    mindlog_mtime: null,
    mindlog_bytes: null,
    step_count: 0,
  })),
  fetchConfig: vi.fn(async () => ({
    root: "/tmp",
    version: "0",
    controls_enabled: false,
    self_update_enabled: false,
    default_send_from: null,
    git_commit: null,
    git_branch: null,
  })),
}));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return { ...mod, fetchUsage, fetchIdentityStatus, fetchConfig };
});

function usage(overrides: Partial<Usage> = {}): Usage {
  return {
    identity: { id: "ada", name: "ada" },
    available: true,
    refreshing: false,
    pending_bytes: 0,
    rows: 1,
    skipped: 0,
    generated: "2026-09-25T10:00:00Z",
    ledger: { rows: 1, skipped: 0, since: "2026-09-23" },
    daily: [
      [
        "2026-09-23",
        {
          rows: 1,
          in_msg: 0,
          out_msg: 0,
          runs: 0,
          reasoning: 0,
          calls: 1,
          unknown: 0,
          in: 100,
          out: 50,
          think: 0,
          source: "ledger",
        },
      ],
    ],
    by_model: { "glm-5": { calls: 1, in: 100, out: 50, think: 0 } },
    totals: { in: 100, out: 50, think: 0, calls: 1, in_msg: 0, out_msg: 0, runs: 0 },
    ...overrides,
  };
}

function renderUsage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/i/ada/usage"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/i/:identityId/usage" element={<UsagePage />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  fetchUsage.mockReset();
  fetchIdentityStatus.mockClear();
  fetchConfig.mockClear();
});
afterEach(cleanup);

describe("usage page admission", () => {
  it("预算已设置、熔断冷却中、存在未知用量：如实展示", async () => {
    fetchUsage.mockResolvedValue(
      usage({
        totals: {
          in: 100,
          out: 50,
          think: 0,
          calls: 3,
          in_msg: 0,
          out_msg: 0,
          runs: 0,
          unknown_calls: 2,
        },
        admission: {
          daily_limit: 5000,
          used_today: 1234,
          consecutive_errors: 3,
          circuit_threshold: 3,
          cooling_until: "2026-09-25T15:25:23.565Z",
        },
      })
    );
    renderUsage();
    expect(await screen.findByText("预算与准入")).toBeDefined();
    expect(screen.getByText(/1,234 \/ 5,000/)).toBeDefined();
    expect(screen.getByText(/冷却中/)).toBeDefined();
    expect(screen.getByText(/2 次调用未返回用量/)).toBeDefined();
  });

  it("预算未设置显示未设置；无未知用量时不显示告警", async () => {
    fetchUsage.mockResolvedValue(
      usage({
        admission: {
          daily_limit: 0,
          used_today: 10,
          consecutive_errors: 0,
          circuit_threshold: 3,
        },
      })
    );
    renderUsage();
    expect(await screen.findByText("未设置")).toBeDefined();
    expect(screen.getByText("正常")).toBeDefined();
    expect(screen.queryByText(/次调用未返回用量/)).toBeNull();
  });

  it("冷却已过、计数未清零：显示等待探测", async () => {
    fetchUsage.mockResolvedValue(
      usage({
        admission: {
          daily_limit: 100,
          used_today: 5,
          consecutive_errors: 3,
          circuit_threshold: 3,
        },
      })
    );
    renderUsage();
    expect(await screen.findByText(/等待探测/)).toBeDefined();
  });

  it("admission 缺失（旧数据）不崩溃", async () => {
    fetchUsage.mockResolvedValue(usage());
    renderUsage();
    expect(await screen.findByText("预算与准入")).toBeDefined();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(2);
  });
});
