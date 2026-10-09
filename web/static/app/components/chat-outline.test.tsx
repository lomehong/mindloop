// 对话目录（回合大纲）：buildTurns 折叠规则 + 组件开合/跳转/不足门槛。
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { buildTurns, ChatOutline } from "~/components/chat-outline";
import { dayLabel, localDay } from "~/lib/format";
import type { ChatMessage } from "~/lib/types";

Element.prototype.scrollIntoView ??= () => {};

afterEach(cleanup);

function msg(over: Partial<ChatMessage>): ChatMessage {
  return {
    ts: "2026-10-06T02:00:00Z",
    step_id: "s-1",
    from: "you",
    to: "ada",
    content: "hi",
    reply_to: null,
    filename: null,
    source_url: null,
    ...over,
  };
}

describe("buildTurns", () => {
  it("我方消息起回合、心智消息并入计数；开头的心智消息自成一条", () => {
    const turns = buildTurns(
      [
        msg({ step_id: "a", from: "ada", content: "主动播报" }),
        msg({ step_id: "b", from: "you", content: "问题一\n第二行" }),
        msg({ step_id: "c", from: "ada", content: "答一" }),
        msg({ step_id: "d", from: "ada", content: "答二" }),
        msg({ step_id: "e", from: "you", content: "问题二" }),
        msg({ step_id: "f", from: "ada", content: "答三" }),
      ],
      "you"
    );
    expect(turns.map((t) => [t.kind, t.replyCount])).toEqual([
      ["mind", 0],
      ["mine", 2],
      ["mine", 1],
    ]);
    // 锚点与首行摘要（不取第二行）。
    expect(turns[0].anchorId).toBe("msg-a");
    expect(turns[1].excerpt).toBe("问题一");
  });

  it("无 step_id 的消息不入目录；长文本截断", () => {
    const long = "很长的消息".repeat(20);
    const turns = buildTurns(
      [
        msg({ step_id: null, from: "you", content: "无锚点" }),
        msg({ step_id: "x", from: "you", content: long }),
      ],
      "you"
    );
    expect(turns.length).toBe(1);
    expect(turns[0].excerpt.length).toBeLessThanOrEqual(43);
    expect(turns[0].excerpt.endsWith("…")).toBe(true);
  });
});

describe("日期标签", () => {
  it("localDay 用本地日期；dayLabel 今天/昨天/MM-DD", () => {
    const now = new Date(2026, 9, 6, 12, 0, 0); // 2026-10-06 本地
    const today = localDay(now.toISOString());
    expect(today).toBe("2026-10-06");
    expect(dayLabel(today!, now)).toBe("今天");
    const yest = localDay(new Date(now.getTime() - 86400_000).toISOString());
    expect(dayLabel(yest!, now)).toBe("昨天");
    expect(dayLabel("2026-10-01", now)).toBe("10-1");
    expect(dayLabel("2025-12-31", now)).toBe("2025-12-31");
  });
});

describe("ChatOutline", () => {
  const six = ["a", "b", "c", "d", "e", "f"].map((id, i) =>
    msg({ step_id: id, from: "you", content: `第${i + 1}问` })
  );

  it("回合不足 5 条时不出现目录按钮", () => {
    render(<ChatOutline messages={six.slice(0, 4)} myName="you" scrollerRef={{ current: null }} />);
    expect(screen.queryByText("目录")).toBeNull();
  });

  it("开浮层 → 点条目滚动定位+闪烁+收起", () => {
    const anchors = ["a", "b", "c", "d", "e", "f"].map((id) => (
      <div key={id} id={`msg-${id}`} />
    ));
    render(
      <>
        {anchors}
        <ChatOutline messages={six} myName="you" scrollerRef={{ current: null }} />
      </>
    );
    fireEvent.click(screen.getByRole("button", { name: "对话目录" }));
    expect(screen.getByText("第3问")).toBeDefined();
    fireEvent.click(screen.getByText("第3问"));
    const anchor = document.getElementById("msg-c");
    expect(anchor?.classList.contains("tl-flash")).toBe(true);
    // 跳转后浮层收起
    expect(screen.queryByText("第3问")).toBeNull();
  });
});
