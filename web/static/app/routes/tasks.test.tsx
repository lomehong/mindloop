// 任务页的行为测试：列表渲染（真实投影字段）、空态、取消/重试命令的
// 参数接线与成功后的重新取数——不做乐观改写，状态一律以 API 为准。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentTask, PendingApproval } from "~/lib/types";
import TasksPage from "~/routes/tasks";

function task(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    task_id: "t-1",
    identity_id: "ada",
    from: "operator",
    client_message_id: "cid-1",
    content: "整理周报",
    status: "queued",
    attempt: 1,
    created_at: "2026-09-25T10:00:00Z",
    updated_at: "2026-09-25T10:00:00Z",
    events: [],
    ...overrides,
  };
}

function approval(overrides: Partial<PendingApproval> = {}): PendingApproval {
  return {
    hash: "abc1234567890abc1234567890abc1234567890abc1234567890abc1234567890",
    script: "rm -rf build/\necho done",
    work_dir: "D:\\work\\x",
    run_id: "run-9",
    created: "2026-09-25T10:00:00Z",
    expires: "2026-09-25T10:10:00Z",
    risks: ["包含删除操作（rm -rf/-f），可能不可逆"],
    ...overrides,
  };
}

const { fetchTasks, cancelTask, retryTask, fetchApprovals, decideApproval } =
  vi.hoisted(() => ({
    fetchTasks: vi.fn(async (): Promise<AgentTask[]> => []),
    cancelTask: vi.fn(
      async (): Promise<AgentTask> => task({ status: "canceling" })
    ),
    retryTask: vi.fn(
      async (): Promise<AgentTask> => task({ status: "queued", attempt: 2 })
    ),
    fetchApprovals: vi.fn(async (): Promise<PendingApproval[]> => []),
    decideApproval: vi.fn(
      async (): Promise<{ hash: string; decision: string }> => ({
        hash: approval().hash,
        decision: "approve",
      })
    ),
  }));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchTasks,
    cancelTask,
    retryTask,
    fetchApprovals,
    decideApproval,
  };
});

function renderTasks() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/talk/ada/tasks"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/talk/:identityId/tasks" element={<TasksPage />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  fetchTasks.mockClear();
  cancelTask.mockClear();
  retryTask.mockClear();
  fetchApprovals.mockClear();
  decideApproval.mockClear();
  fetchTasks.mockResolvedValue([]);
  fetchApprovals.mockResolvedValue([]);
});
afterEach(cleanup);

describe("tasks page", () => {
  it("渲染投影字段：状态、attempt 代次、结果与运行链接", async () => {
    fetchTasks.mockResolvedValue([
      task({
        task_id: "t-run",
        status: "running",
        attempt: 2,
        run_id: "run-77",
        content: "跑一次冒烟",
      }),
      task({
        task_id: "t-done",
        status: "succeeded",
        result: "周报已生成：weekly.md",
      }),
    ]);
    renderTasks();

    expect(await screen.findByText("执行中")).toBeDefined();
    expect(screen.getByText("第 2 次执行")).toBeDefined();
    expect(screen.getByText("已完成")).toBeDefined();
    expect(screen.getByText("周报已生成：weekly.md")).toBeDefined();
    const link = screen.getByRole("link", { name: "运行记录" });
    expect(link.getAttribute("href")).toBe("/i/ada/mindlog");
  });

  it("空态给出回到对话创建任务的指引", async () => {
    renderTasks();
    expect(await screen.findByText(/还没有任务/)).toBeDefined();
  });

  it("点取消走 cancelTask(identity, task_id)，成功后重新取列表", async () => {
    fetchTasks.mockResolvedValue([task({ task_id: "t-1" })]);
    renderTasks();

    fireEvent.click(await screen.findByRole("button", { name: "取消" }));
    await waitFor(() =>
      expect(cancelTask).toHaveBeenCalledWith("ada", "t-1")
    );
    // 命令成功后 invalidate 触发列表重取（初始 1 次 + 成功 1 次）。
    await waitFor(() => expect(fetchTasks.mock.calls.length).toBeGreaterThan(1));
  });

  it("渲染待批脚本：正文、工作目录、风险；批准走 decideApproval(identity, hash, true) 并重取", async () => {
    const ap = approval();
    fetchApprovals.mockResolvedValue([ap]);
    renderTasks();

    expect(await screen.findByText("等待批准")).toBeDefined();
    expect(screen.getByText(/rm -rf build\//)).toBeDefined();
    expect(screen.getByText("D:\\work\\x")).toBeDefined();
    expect(screen.getByText(/包含删除操作/)).toBeDefined();

    fireEvent.click(screen.getByRole("button", { name: "批准" }));
    await waitFor(() =>
      expect(decideApproval).toHaveBeenCalledWith("ada", ap.hash, true)
    );
    await waitFor(() =>
      expect(fetchApprovals.mock.calls.length).toBeGreaterThan(1)
    );
  });

  it("拒绝走 decideApproval(identity, hash, false)", async () => {
    fetchApprovals.mockResolvedValue([approval()]);
    renderTasks();

    fireEvent.click(await screen.findByRole("button", { name: "拒绝" }));
    await waitFor(() =>
      expect(decideApproval).toHaveBeenCalledWith("ada", approval().hash, false)
    );
  });

  it("无待批时不渲染等待批准区块", async () => {
    renderTasks();
    await screen.findByText(/还没有任务/);
    expect(screen.queryByText("等待批准")).toBeNull();
  });
});
