import { describe, expect, it } from "vitest";

import type { Identity, PendingApproval } from "~/lib/types";

import { signalsFor } from "./home";

// 回归：待审批的「去处理」曾误链 /talk（手机 PWA 面）——必须落在
// 桌面身份任务 tab（/i/:id/tasks），见 ui-language.md §5.1。
describe("signalsFor 去处理去处", () => {
  const approval = () =>
    [
      { hash: "h1", created: new Date(Date.now() - 7_200_000).toISOString() },
    ] as unknown as PendingApproval[];

  it("待审批 → 桌面任务 tab", () => {
    const identity = { id: "ada", name: "ada" } as unknown as Identity;
    const rows = signalsFor(identity, undefined, approval(), undefined);
    expect(rows.map((row) => row.to)).toEqual(["/i/ada/tasks"]);
  });

  it("身份 id 编码后进路径", () => {
    const identity = { id: "团队~ada", name: "团队~ada" } as unknown as Identity;
    const rows = signalsFor(identity, undefined, approval(), undefined);
    expect(rows[0].to).toBe(`/i/${encodeURIComponent("团队~ada")}/tasks`);
  });

  it("无待审批则无信号行", () => {
    const identity = { id: "ada", name: "ada" } as unknown as Identity;
    expect(signalsFor(identity, undefined, [], undefined)).toEqual([]);
  });
});
