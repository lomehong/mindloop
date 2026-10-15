// 对话条行为机械验证：①默认收起只剩输入框 ②悬停展开且定位到最新
// （距底部 ≤2px）③移开即收起。跑法：node uitest/verify-talkstrip.mjs
// （仪表盘需已启动；身份取 --identity 参数，默认 tester）。
import { chromium } from "playwright-core";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const args = process.argv.slice(2);
const arg = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 && args[i + 1] && !args[i + 1].startsWith("--") ? args[i + 1] : def;
};
const BASE = arg("base", "http://127.0.0.1:8080");
const IDENT = arg("identity", "tester");
const ART = resolve(".zcode/uitest-artifacts");
mkdirSync(ART, { recursive: true });

const browser = await chromium.launch({ channel: "msedge", headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 1600, height: 900 } });
  await page.goto(`${BASE}/i/${IDENT}`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector('[data-testid="talk-strip"]', { timeout: 20000 });

  // ① 默认（未悬停）：收起，日志不渲染
  const open0 = await page.getAttribute('[data-testid="talk-strip"]', "data-open");
  if (open0 !== "false") throw new Error(`初始应收起，data-open=${open0}`);
  const logCount = await page.locator('[data-testid="talk-strip-log"]').count();
  if (logCount !== 0) throw new Error(`初始不应渲染日志，count=${logCount}`);
  await page.screenshot({ path: resolve(ART, "talkstrip-collapsed.png") });

  // ② 悬停展开：等待历史消息渲染出足够高度，断言定位在最新
  await page.hover('[data-testid="talk-strip-input"]');
  await page.waitForSelector('[data-testid="talk-strip-log"]', { timeout: 5000 });
  await page.waitForFunction(
    () => {
      const log = document.querySelector('[data-testid="talk-strip-log"]');
      return log && log.scrollHeight > 200;
    },
    { timeout: 15000 },
  );
  await page.waitForTimeout(300);
  const gap = await page.evaluate(() => {
    const log = document.querySelector('[data-testid="talk-strip-log"]');
    return log.scrollHeight - log.scrollTop - log.clientHeight;
  });
  if (gap > 2) throw new Error(`展开后应定位最新，距底部 ${gap}px`);
  const open1 = await page.getAttribute('[data-testid="talk-strip"]', "data-open");
  if (open1 !== "true") throw new Error(`悬停应展开，data-open=${open1}`);
  await page.screenshot({ path: resolve(ART, "talkstrip-expanded.png") });

  // ③ 移开（滚到历史中间再移开，验证收起不残留）
  await page.evaluate(() => {
    const log = document.querySelector('[data-testid="talk-strip-log"]');
    log.scrollTop = 0;
  });
  await page.mouse.move(20, 300);
  await page.waitForTimeout(200);
  const open2 = await page.getAttribute('[data-testid="talk-strip"]', "data-open");
  if (open2 !== "false") throw new Error(`移开后应收起，data-open=${open2}`);
  const logAfter = await page.locator('[data-testid="talk-strip-log"]').count();
  if (logAfter !== 0) throw new Error("移开后日志应卸载");

  // ④ 再移回：重新展开仍定位最新（收放循环不劣化）
  await page.hover('[data-testid="talk-strip-input"]');
  await page.waitForSelector('[data-testid="talk-strip-log"]', { timeout: 5000 });
  await page.waitForFunction(
    () => {
      const log = document.querySelector('[data-testid="talk-strip-log"]');
      return log && log.scrollHeight > 200;
    },
    { timeout: 15000 },
  );
  await page.waitForTimeout(300);
  const gap2 = await page.evaluate(() => {
    const log = document.querySelector('[data-testid="talk-strip-log"]');
    return log.scrollHeight - log.scrollTop - log.clientHeight;
  });
  if (gap2 > 2) throw new Error(`再展开应重新定位最新，距底部 ${gap2}px`);

  writeFileSync(resolve(ART, "talkstrip-verify.json"), JSON.stringify({ pass: true, gap, gap2 }, null, 2));
  console.log(`TALKSTRIP VERIFY PASS (gap=${gap}px, regap=${gap2}px)`);
} finally {
  await browser.close();
}
