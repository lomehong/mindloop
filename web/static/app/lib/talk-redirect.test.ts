import { describe, expect, it } from "vitest";

import { desktopPathFromTalk } from "./talk-redirect";

describe("desktopPathFromTalk", () => {
  it("落地页与未知路径回工作台", () => {
    expect(desktopPathFromTalk("/talk")).toBe("/");
    expect(desktopPathFromTalk("/talk/")).toBe("/");
    expect(desktopPathFromTalk("/talk/ada/nope")).toBe("/");
  });

  it("对话页回桌面身份对话 tab", () => {
    expect(desktopPathFromTalk("/talk/ada")).toBe("/i/ada/chat");
  });

  it("任务页回桌面身份任务 tab", () => {
    expect(desktopPathFromTalk("/talk/ada/tasks")).toBe("/i/ada/tasks");
  });

  it("身份段保持已编码原样", () => {
    expect(desktopPathFromTalk("/talk/%E5%9B%A2%E9%98%9F~ada")).toBe(
      "/i/%E5%9B%A2%E9%98%9F~ada/chat"
    );
  });
});
