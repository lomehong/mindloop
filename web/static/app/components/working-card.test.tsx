// WorkingCard：实时活动卡片的渲染测试——标题、步骤类型到图标中文标签
// 的映射、excerpt 单行截断、相对时间，以及退场（working=false 且无
// activity 时整卡不渲染）。

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { WorkingCard } from "~/components/working-card";
import type { StepActivity } from "~/lib/use-chat";

afterEach(cleanup);

const NOW = new Date();

function step(
  stepId: string,
  stepType: string,
  excerpt: string
): StepActivity {
  const ts = NOW.toISOString();
  return { stepId, stepType, ts, excerpt, arrivedAt: NOW.getTime() };
}

describe("WorkingCard", () => {
  it("working=true 渲染标题「{name} 正在工作中…」", () => {
    render(<WorkingCard name="ada" working stepTotal={0} sentAt={null} activity={[]} />);
    expect(screen.getByText("ada 正在工作中…")).toBeDefined();
  });

  it("按 type 映射图标与中文标签，excerpt 截断展示 + 相对时间", () => {
    render(
      <WorkingCard
        name="ada"
        working
        stepTotal={6}
        sentAt={Date.now() - 30_000}
        activity={[
          step("a1", "reasoning", "拆解任务"),
          step("a2", "action", "ls -la"),
          step("a3", "shell-output", "total 0"),
          step("a4", "observation", "文件为空"),
          step("a5", "final", "搞定了"),
          step("a6", "error", "炸了"),
        ]}
      />
    );
    expect(screen.getByText("💭")).toBeDefined();
    expect(screen.getByText("思考")).toBeDefined();
    expect(screen.getByText("⚙️")).toBeDefined();
    expect(screen.getByText("执行命令")).toBeDefined();
    expect(screen.getByText("🖥")).toBeDefined();
    expect(screen.getByText("命令输出")).toBeDefined();
    expect(screen.getByText("📄")).toBeDefined();
    expect(screen.getByText("观察")).toBeDefined();
    expect(screen.getByText("✅")).toBeDefined();
    expect(screen.getByText("完成")).toBeDefined();
    expect(screen.getByText("❌")).toBeDefined();
    expect(screen.getByText("出错")).toBeDefined();
    // excerpt 原样出现。
    expect(screen.getByText("拆解任务")).toBeDefined();
    expect(screen.getByText("ls -la")).toBeDefined();
    // 刚写入的时间戳 → 「刚刚」。最新一条（a6）在阶段行展示、不进列表，
    // 所以列表只有 5 行相对时间。
    const times = screen.getAllByText("刚刚");
    expect(times.length).toBe(5);
  });

  it("未知类型显示 type 原文（协议演进容错）", () => {
    render(
      <WorkingCard
        name="ada"
        working
        stepTotal={1}
        sentAt={Date.now() - 10_000}
        activity={[step("u1", "tp-thought", "旧类型步骤")]}
      />
    );
    // 没有 meta 条目：无图标（渲染占位 •），标签回退为 type 原文。
    expect(screen.getByText("tp-thought")).toBeDefined();
    expect(screen.getByText("旧类型步骤")).toBeDefined();
    expect(screen.getByText("•")).toBeDefined();
  });

  it("working=false 且 activity 已清空 → 整卡不渲染", () => {
    const { container } = render(
      <WorkingCard name="ada" working={false} stepTotal={0} sentAt={null} activity={[]} />
    );
    expect(container.querySelector("div")).toBeNull();
  });

  it("working=false 但 activity 尚在（淡出窗口期）→ 卡片仍渲染、透明度归零", () => {
    const { container } = render(
      <WorkingCard
        name="ada"
        working={false}
        stepTotal={1}
        sentAt={Date.now() - 5_000}
        activity={[step("a1", "reasoning", "想一想")]}
      />
    );
    const root = container.firstElementChild as HTMLElement | null;
    expect(root).not.toBeNull();
    expect(root?.getAttribute("data-working")).toBe("false");
    expect(root?.className).toContain("opacity-0");
  });
});
