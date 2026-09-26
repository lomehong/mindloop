// TaskCard 的行为测试：状态徽章与真实字段呈现（attempt、run 链接、
// 结果与失败原因、来源关联）、按状态给出的操作（取消/重试），以及
// 回调携带完整任务对象（父层据此做幂等命令）。

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";

import { TaskCard } from "~/components/task-card";
import type { AgentTask } from "~/lib/types";

afterEach(cleanup);

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

function renderCard(element: ReactElement) {
  return render(<MemoryRouter>{element}</MemoryRouter>);
}

describe("TaskCard", () => {
  it("queued：排队中徽章 + 内容 + 取消按钮；点取消回传任务", () => {
    const onCancel = vi.fn();
    const item = task();
    renderCard(<TaskCard task={item} onCancel={onCancel} onRetry={vi.fn()} />);

    expect(screen.getByText("排队中")).toBeDefined();
    expect(screen.getByText("整理周报")).toBeDefined();
    // 两个回调都已提供：重试缺席是状态逻辑（排队中不可重试），不是没接线。
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(onCancel).toHaveBeenCalledWith(item);
  });

  it("running：执行中 + attempt 代次 + 运行链接；无重试", () => {
    renderCard(
      <TaskCard
        task={task({
          status: "running",
          attempt: 2,
          run_id: "run-77",
        })}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
      />
    );

    expect(screen.getByText("执行中")).toBeDefined();
    expect(screen.getByText("第 2 次执行")).toBeDefined();
    const link = screen.getByRole("link", { name: "运行记录" });
    expect(link.getAttribute("href")).toBe("/i/ada/mindlog");
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("succeeded：呈现结果文本，不再提供操作", () => {
    renderCard(
      <TaskCard
        task={task({
          status: "succeeded",
          result: "周报已生成：weekly.md",
          result_kind: "text",
        })}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
      />
    );

    expect(screen.getByText("已完成")).toBeDefined();
    expect(screen.getByText("周报已生成：weekly.md")).toBeDefined();
    expect(screen.queryByRole("button", { name: "取消" })).toBeNull();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("failed：呈现失败原因；点重试回传任务", () => {
    const onRetry = vi.fn();
    const item = task({
      status: "failed",
      reason: "模型调用超时",
    });
    renderCard(<TaskCard task={item} onCancel={vi.fn()} onRetry={onRetry} />);

    expect(screen.getByText("失败")).toBeDefined();
    expect(screen.getByText("模型调用超时")).toBeDefined();
    expect(screen.queryByRole("button", { name: "取消" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(onRetry).toHaveBeenCalledWith(item);
  });

  it("interrupted/budget_exceeded：可重试、不可取消", () => {
    renderCard(
      <TaskCard
        task={task({
          status: "interrupted",
          reason: "进程重启导致执行中断",
        })}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
      />
    );
    expect(screen.getByText("已中断")).toBeDefined();
    expect(screen.getByText("进程重启导致执行中断")).toBeDefined();
    expect(screen.getByRole("button", { name: "重试" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "取消" })).toBeNull();
  });

  it("source_step_id 存在时标注来源关联", () => {
    renderCard(<TaskCard task={task({ source_step_id: "s-9" })} />);
    expect(screen.getByText("从消息转来")).toBeDefined();
  });
});
