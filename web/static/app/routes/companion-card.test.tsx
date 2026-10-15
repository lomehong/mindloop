// 同行面渲染契约测试（avatar.md §6.2）：三轴词表（能力/信任/传承——
// 机制账钉死，不出现阶段/百分比类假指标）、今日账目一句话、共同成长
// 账三栏顺序（它为你/你为它/一起长出）与窗口诚实脚注（窗口起点落在
// 今日之内必须标注，不得写成"今日共 N 件"）。

import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it } from "vitest";

import { CompanionCard, GrowthLedgerCard } from "./home";
import { ledgerFor } from "~/lib/growth-ledger";
import type { Identity, NormalizedStep } from "~/lib/types";

afterEach(cleanup);

const NOW = (() => {
  const d = new Date();
  d.setHours(12, 0, 0, 0);
  return d;
})();

function todayTs(h: number): string {
  const d = new Date(NOW);
  d.setHours(h, 0, 0, 0);
  return d.toISOString();
}

function step(partial: Partial<NormalizedStep> & { step_id: string }): NormalizedStep {
  return {
    ts: todayTs(10),
    type: "final",
    source: null,
    preview: "",
    raw: {},
    run_id: null,
    ...partial,
  };
}

function identity(partial: Partial<Identity>): Identity {
  return {
    id: "ada",
    name: "ada",
    live: true,
    thinkers_total: 2,
    thinkers_active: 1,
    last_activity_ts: todayTs(9),
    dispatcher: { running: true },
    ...partial,
  } as Identity;
}

function renderCard(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe("CompanionCard", () => {
  const base = {
    identity: identity({}),
    usage: undefined,
    approvals: undefined,
    activity: undefined,
    tasks: undefined,
    skills: undefined,
    memories: undefined,
    taste: undefined,
    env: undefined,
    mindlog: undefined,
  };

  it("三轴词表：能力/信任/传承（机制账词表钉死）", () => {
    renderCard(<CompanionCard slot={base} controlsEnabled={false} />);
    for (const label of ["能力", "信任", "传承"]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    // 样张阶段 chip（2026-10-15 拍板）：有投影段名，但绝不带百分比
    //（avatar.md §8.1 反游戏化——红线禁的是假进度数字，不是段名）。
    expect(screen.queryByText(/\d+\s*%/)).toBeNull();
  });

  it("今日账目一句话：有账报账，无账说无", () => {
    const mindlog = {
      steps: [
        step({ step_id: "f1", raw: { context_kind: "autonomous" }, preview: "自己修好了 CI" }),
        step({ step_id: "t1", type: "taste" as const, raw: { signal: "approve" } }),
      ],
    };
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = (ui: React.ReactElement) => (
      <QueryClientProvider client={qc}>
        <MemoryRouter>{ui}</MemoryRouter>
      </QueryClientProvider>
    );
    const { rerender } = render(
      wrapper(
        <CompanionCard
          slot={{ ...base, mindlog: mindlog as never }}
          controlsEnabled={false}
        />
      )
    );
    expect(screen.getByText(/它自己收了 1 件事/).textContent).toContain("你过手 1 次");

    rerender(
      wrapper(
        <CompanionCard
          slot={{ ...base, mindlog: { steps: [] } as never }}
          controlsEnabled={false}
        />
      )
    );
    expect(screen.getByText("今日还没有新账")).toBeTruthy();
  });
});

describe("GrowthLedgerCard", () => {
  it("三栏顺序：它为你 / 你为它 / 一起长出，事件带身份署名", () => {
    const ledgers = [
      ledgerFor({
        identityId: "ada",
        identityName: "ada",
        now: NOW,
        steps: [
          step({ step_id: "f1", raw: { context_kind: "autonomous" }, preview: "自己修好了 CI" }),
          step({ step_id: "t1", type: "taste", raw: { signal: "undo", cause: "提案多余" } }),
        ],
        memories: [
          {
            name: "m1",
            mtime: 0,
            id: "1",
            summary: "commit 备注用中文",
            type: "偏好",
            created: todayTs(11),
            slug: "m1",
          },
        ],
      }),
    ];
    renderCard(<GrowthLedgerCard ledgers={ledgers} />);
    const kickers = ["它为你", "你为它", "一起长出"].map((k) => screen.getByText(k));
    expect(kickers).toHaveLength(3);
    expect(screen.getByText(/自己修好了 CI/)).toBeTruthy();
    expect(screen.getByText("纠错 · 提案多余")).toBeTruthy();
    expect(screen.getByText("commit 备注用中文")).toBeTruthy();
  });

  it("窗口诚实：起点在今日之内时脚注标注'未含'，不写'覆盖今日'", () => {
    const ledgers = [
      ledgerFor({
        identityId: "ada",
        identityName: "ada",
        now: NOW,
        steps: [step({ step_id: "s0", ts: todayTs(11) })],
      }),
    ];
    renderCard(<GrowthLedgerCard ledgers={ledgers} />);
    const note = screen.getByText(/窗口起点在今日之内/);
    expect(note.textContent).not.toContain("覆盖今日");
  });

  it("空账：三栏在，各栏说'今日还没有'", () => {
    renderCard(<GrowthLedgerCard ledgers={[]} />);
    expect(screen.getAllByText("今日还没有")).toHaveLength(3);
    // 空账同样要报窗口——不冒充"覆盖今日"。
    expect(screen.getByText(/最近 1000 步/)).toBeTruthy();
  });

  it("事件行带时间与身份名", () => {
    const ledgers = [
      ledgerFor({
        identityId: "ada",
        identityName: "ada",
        now: NOW,
        steps: [step({ step_id: "f1", ts: todayTs(9), raw: { context_kind: "autonomous" }, preview: "晨巡完成" })],
      }),
    ];
    renderCard(<GrowthLedgerCard ledgers={ledgers} />);
    const cell = screen.getByText("晨巡完成").closest("div");
    expect(cell?.textContent).toContain("ada");
    expect(cell?.textContent).toMatch(/^\d{2}:\d{2}/);
    void within;
  });
});
