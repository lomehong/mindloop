// 档案卡底部快捷链接（去重保留方案）：非「运行」分区显示
// 健康/用量/思考者；身处「运行」分区时隐藏——子段同屏，不重复。

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { IdentityDossier } from "~/components/identity-dossier";

vi.mock("~/components/pet/pet-dock", () => ({
  PetDock: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
}));

vi.mock("~/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...mod,
    fetchIdentities: vi.fn(async () => []),
    fetchActivity: vi.fn(async () => null),
    fetchIdentityStatus: vi.fn(async () => null),
    fetchThinkers: vi.fn(async () => null),
    fetchUsage: vi.fn(async () => null),
  };
});

function renderAt(path: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={client}>
        <IdentityDossier />
      </QueryClientProvider>
    </MemoryRouter>
  );
}

afterEach(cleanup);

describe("identity dossier footer links", () => {
  it("非运行分区：显示 健康/用量/思考者 三个快捷链接", () => {
    renderAt("/i/ada/chat");
    expect(screen.getByRole("link", { name: "健康" })).toBeDefined();
    expect(screen.getByRole("link", { name: "用量" })).toBeDefined();
    const thinkers = screen.getByRole("link", { name: "思考者" });
    expect(thinkers.getAttribute("href")).toBe("/i/ada/run/thinkers");
  });

  it("运行分区：隐藏快捷链接（子段同屏，不重复）", () => {
    renderAt("/i/ada/run/usage");
    expect(screen.queryByRole("link", { name: "健康" })).toBeNull();
    expect(screen.queryByRole("link", { name: "用量" })).toBeNull();
    expect(screen.queryByRole("link", { name: "思考者" })).toBeNull();
  });
});
