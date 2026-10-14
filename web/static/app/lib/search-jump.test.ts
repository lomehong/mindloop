import { describe, expect, it } from "vitest";

import { jumpTargetFor } from "./search-jump";

// 搜索命中直达的目标换算（2026-10-14 审查 #7 回归）：目标曾被写死
// `${base}?step=`——舞台成为 index 路由后该参数无人消费，跳转静默
// 失效。轨迹区必须落泳道 /traj（TimelineView 是 ?step= 唯一消费者）。
describe("jumpTargetFor", () => {
  it("轨迹区命中 → 泳道 /traj?step=", () => {
    expect(jumpTargetFor("traj", "/i/ada", "s123")).toBe(
      "/i/ada/traj?step=s123"
    );
  });

  it("step_id 经 URL 编码", () => {
    expect(jumpTargetFor("traj", "/i/ada", "a/b&c")).toBe(
      "/i/ada/traj?step=a%2Fb%26c"
    );
  });

  it("非轨迹区（对话/记忆）→ 日志流定位", () => {
    expect(jumpTargetFor("chat", "/i/ada", "s1")).toBe("/i/ada/log?step=s1");
    expect(jumpTargetFor("memories", "/i/ada", "s2")).toBe(
      "/i/ada/log?step=s2"
    );
  });
});
