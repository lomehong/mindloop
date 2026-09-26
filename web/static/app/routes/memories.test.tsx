// 记忆页的修订/失效行为测试：活动条目可操作、失效条目只读；
// 修订走 reviseMemory(identity, id, 新正文)，失效走确认对话框 +
// invalidateMemory(identity, id)——成功后都由 invalidate 触发重新取数。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { IdentityStatus, MemoryInfo } from "~/lib/types";
import MemoriesPage from "~/routes/memories";

function memory(overrides: Partial<MemoryInfo> = {}): MemoryInfo {
  return {
    name: "000001_a1b2c3d4.md",
    mtime: 1758800000000,
    id: "a1b2c3d4",
    summary: "用户喜欢黑咖啡",
    type: "fact",
    created: "2026-09-25T10:00:00Z",
    slug: "a1b2c3d4",
    status: "",
    ...overrides,
  };
}

const {
  fetchIdentityStatus,
  fetchMemories,
  fetchMemory,
  reviseMemory,
  invalidateMemory,
} = vi.hoisted(() => ({
  fetchIdentityStatus: vi.fn(
    async (): Promise<IdentityStatus> => ({
      live: false,
      pid_alive: false,
      dispatcher_pid: null,
      mindlog_mtime: null,
      mindlog_bytes: null,
      step_count: 0,
    })
  ),
  fetchMemories: vi.fn(async (): Promise<MemoryInfo[]> => []),
  fetchMemory: vi.fn(
    async (): Promise<{ name: string; content: string }> => ({
      name: "000001_a1b2c3d4.md",
      content:
        "---\nid: a1b2c3d4\ntype: fact\nsummary: 用户喜欢黑咖啡\n---\n用户喜欢黑咖啡，不加糖\n",
    })
  ),
  reviseMemory: vi.fn(async () => ({
    ok: true,
    id: "ffee1122",
    supersedes: "a1b2c3d4",
  })),
  invalidateMemory: vi.fn(async () => ({
    ok: true,
    id: "a1b2c3d4",
    status: "invalid",
  })),
}));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchIdentityStatus,
    fetchMemories,
    fetchMemory,
    reviseMemory,
    invalidateMemory,
  };
});

function renderMemories() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/i/ada/memories"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/i/:identityId/memories" element={<MemoriesPage />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  for (const fn of [
    fetchIdentityStatus,
    fetchMemories,
    fetchMemory,
    reviseMemory,
    invalidateMemory,
  ]) {
    fn.mockClear();
  }
  fetchMemories.mockResolvedValue([memory()]);
});

afterEach(cleanup);

describe("memories page", () => {
  it("活动条目可修订/失效；切到失效条目后只读", async () => {
    fetchMemories.mockResolvedValue([
      memory(),
      memory({
        name: "000002_deadbeef.md",
        id: "deadbeef",
        slug: "deadbeef",
        summary: "旧事实",
        status: "invalid",
      }),
    ]);
    renderMemories();

    // 默认选中活动条目——两个操作可用。
    expect(await screen.findByRole("button", { name: "修订" })).toBeDefined();
    expect(screen.getByRole("button", { name: "失效" })).toBeDefined();

    // 切到失效条目——只读：操作按钮消失。
    fireEvent.click(await screen.findByText("旧事实"));
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "修订" })).toBeNull()
    );
    expect(screen.queryByRole("button", { name: "失效" })).toBeNull();
  });

  it("修订：预填正文、提交 reviseMemory(identity, id, 新正文)", async () => {
    renderMemories();
    fireEvent.click(await screen.findByRole("button", { name: "修订" }));

    const box = (await screen.findByLabelText(
      "修订内容"
    )) as HTMLTextAreaElement;
    expect(box.value).toContain("用户喜欢黑咖啡");
    fireEvent.change(box, { target: { value: "用户喜欢拿铁，半糖" } });
    fireEvent.click(screen.getByRole("button", { name: "保存修订" }));

    await waitFor(() =>
      expect(reviseMemory).toHaveBeenCalledWith(
        "ada",
        "a1b2c3d4",
        "用户喜欢拿铁，半糖"
      )
    );
  });

  it("失效：确认对话框后调 invalidateMemory(identity, id)", async () => {
    renderMemories();
    fireEvent.click(await screen.findByRole("button", { name: "失效" }));

    fireEvent.click(await screen.findByRole("button", { name: "确认失效" }));
    await waitFor(() =>
      expect(invalidateMemory).toHaveBeenCalledWith("ada", "a1b2c3d4")
    );
  });
});
