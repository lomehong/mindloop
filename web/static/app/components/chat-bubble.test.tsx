// StreamingChatBubble：回复生成期间的渐进气泡。
// 关键约束——流式期间正文必须走纯文本渲染（whitespace-pre-wrap），
// 半截 Markdown 绝不能进 Markdown 渲染器造成反复重排闪烁；text 为空时
// 显示「正在输入」点（由 SSE replying 真信号驱动）。

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { StreamingChatBubble } from "~/components/chat-bubble";

afterEach(cleanup);

describe("StreamingChatBubble", () => {
  it("没有 delta 文本时显示「正在输入」点（replying 状态）", () => {
    render(<StreamingChatBubble text="" variant="talk" />);
    const status = screen.getByRole("status", { name: "正在输入回复" });
    expect(status).toBeDefined();
  });

  it("正文为纯文本渐进渲染：Markdown 语法保持字面量，不产生 <strong>", () => {
    const { container } = render(
      <StreamingChatBubble
        text={"**加粗** 未闭合的代码块：\n```ts\nconst a = 1"}
        variant="talk"
      />
    );
    // 字面量出现在 DOM 里（含星号与围栏）——没有走 Markdown 解析。
    expect(screen.getByText(/未闭合的代码块/)).toBeDefined();
    expect(container.querySelector("strong")).toBeNull();
    expect(container.querySelector("code")).toBeNull();
    expect(container.querySelector("pre")).toBeNull();
  });

  it("文本节点是 whitespace-pre-wrap（保留换行/空格的原样语义）", () => {
    const { container } = render(
      <StreamingChatBubble text={"第一行\n第二行"} variant="desktop" />
    );
    const body = container.querySelector(".whitespace-pre-wrap");
    expect(body).not.toBeNull();
    expect(body?.textContent).toBe("第一行\n第二行");
  });
});
