// 年轮 token 快照（docs/designs/ui-language.md §9.3）：亮暗两套变量值锁定，
// 防手滑改色绕过设计口径。改色必须同时改本文件——让"改色"成为一个
// 有意识的动作，而不是顺手一调。
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// vitest.run 的 cwd 是 web/static；兜底候选覆盖从仓库根启动的情况。
const cssPath = [
  resolve(process.cwd(), "app/app.css"),
  resolve(process.cwd(), "web/static/app/app.css"),
].find((p) => existsSync(p));
if (!cssPath) throw new Error("app.css 未找到（请在 web/static 下跑 npm run test）");
const css = readFileSync(cssPath, "utf8");

function vars(selector: string): Record<string, string> {
  const pattern = selector.replace(".", "\\.");
  // 同选择器可能出现多个块（如早期只带 color-scheme 的 .dark），
  // 取含 --bg 的那个——即年轮令牌块。
  for (const match of css.matchAll(new RegExp(`${pattern}\\s*\\{([^}]*)\\}`, "g"))) {
    const out: Record<string, string> = {};
    for (const m of match[1].matchAll(/--([a-z0-9-]+)\s*:\s*([^;]+);/g)) {
      out[m[1]] = m[2].trim();
    }
    if (out.bg) return out;
  }
  throw new Error(`app.css 未找到 ${selector} 年轮令牌块`);
}

const light = vars(":root");
const dark = vars(".dark");

const LOCKED: Record<string, Record<string, string>> = {
  light: {
    bg: "#eff0e8",
    surface: "#fbfcf7",
    "surface-2": "#e8ebe1",
    fg: "#20271f",
    faint: "#8c968b",
    moss: "#5f9147",
    lake: "#3e7c93",
    plum: "#7c5f9e",
    resin: "#a9791e",
    clay: "#b34e3a",
    primary: "#46853a",
  },
  dark: {
    bg: "#121814",
    surface: "#171f1a",
    "surface-2": "#1f2922",
    fg: "#e6eadf",
    faint: "#6b7767",
    moss: "#6fa24e",
    lake: "#79adc2",
    plum: "#b79bd1",
    resin: "#d9a84e",
    clay: "#d4735e",
    primary: "#8fc166",
  },
};

describe("年轮 token 快照（亮暗锁定）", () => {
  for (const theme of ["light", "dark"] as const) {
    const actual = theme === "light" ? light : dark;
    for (const [name, value] of Object.entries(LOCKED[theme])) {
      it(`${theme} --${name} = ${value}`, () => {
        expect(actual[name]).toBe(value);
      });
    }
  }

  it("亮暗是两套独立色板（防误抄）", () => {
    expect(light.bg).not.toBe(dark.bg);
    expect(light.primary).not.toBe(dark.primary);
    expect(light.clay).not.toBe(dark.clay);
  });

  it("shadcn 别名层指向年轮令牌", () => {
    for (const theme of [light, dark]) {
      expect(theme.background).toBe("var(--bg)");
      expect(theme.card).toBe("var(--surface)");
      expect(theme.destructive).toBe("var(--clay)");
      expect(theme.border).toBe("var(--line)");
      expect(theme["muted-foreground"]).toBeTruthy();
    }
  });
});
