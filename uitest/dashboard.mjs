// 仪表盘人工级 UI 场景库：无头 Edge（系统自带，经 playwright-core
// channel:'msedge'，零浏览器下载）真实点击、填表、断言、截图。
//
// 用法（仪表盘需已启动）：
//   node uitest/dashboard.mjs                       # 全部场景
//   node uitest/dashboard.mjs --scenario sensors    # 单场景
//   node uitest/dashboard.mjs --base http://127.0.0.1:8080 --identity qa-e2e
//
// 输出协议（测试子智能体消费）：每场景一行
//   SCENARIO <name> PASS|FAIL <耗时ms> <原因/证据>
// 任一 FAIL → 退出码 1。截图落 --artifacts 目录（默认 .zcode/uitest-artifacts）。
//
// 红线：只对本地仪表盘（127.0.0.1）操作；表单提交只用测试身份；
// 不输入任何真实凭据。
import { chromium } from "playwright-core";
import { mkdirSync, writeFileSync } from "node:fs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const args = process.argv.slice(2);
const arg = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 && args[i + 1] && !args[i + 1].startsWith("--") ? args[i + 1] : def;
};
const BASE = arg("base", "http://127.0.0.1:8080");
const IDENT = arg("identity", "qa-e2e");
const ONLY = arg("scenario", "all");
const ART = resolve(arg("artifacts", ".zcode/uitest-artifacts"));
mkdirSync(ART, { recursive: true });

const results = [];
let page, browser, ctx;

async function shot(name) {
  const p = join(ART, `${name}.png`);
  await page.screenshot({ path: p, fullPage: true });
  return p;
}

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

async function openIdentity(tabPath = "") {
  await page.goto(`${BASE}/i/${IDENT}${tabPath}`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("body >> text=身 · 触达", { timeout: 10000 }).catch(() => {});
  await page.waitForLoadState("networkidle").catch(() => {});
}

// —— 场景 ————————————————————————————————————————————————

// S12.1 首页身份列表：两个身份行都可见。
async function home() {
  await page.goto(`${BASE}/`, { waitUntil: "networkidle" });
  const text = await page.locator("body").innerText();
  assert(text.includes("ada"), "首页缺少 ada 身份行");
  assert(text.includes(IDENT), `首页缺少 ${IDENT} 身份行`);
  assert(text.includes("新建身份"), "首页缺少『新建身份』入口");
  await shot("S12.1-home");
}

// S12.2 主导航 tab 逐个点击：每页渲染出实质内容（非空白壳）。
// 路径以 identity-tabs.tsx 为准（时间线=身份根空路径）。
async function navTabs() {
  const tabs = [
    ["", /时间线|思维流|步骤|trajectory|思维/i],
    ["/memories", /记忆|memor|检索|无记忆|添加/i],
    ["/schedule", /日程|schedule|暂无|任务/i],
    ["/sensors", /感知|感官|眼|耳|鼻|舌|身/i],
    ["/connections", /连接|连接器|渠道|connector|暂无/i],
    ["/config", /配置|env|模型|身份/i],
  ];
  for (const [path, re] of tabs) {
    await openIdentity(path);
    // 等 SPA 完成挂载：页面出现任一链接即视为骨架就绪。
    await page.waitForSelector("a", { timeout: 8000 });
    const text = await page.locator("body").innerText();
    assert(text.trim().length > 50, `tab ${path || "(根)"} 内容过少（疑似白屏）`);
    assert(!/请求的页面不存在/.test(text), `tab ${path || "(根)"} 404（路径漂移？）`);
    assert(re.test(text), `tab ${path || "(根)"} 未出现预期文案（${re}）`);
    await shot(`S12.2-tab${(path || "-root").replace(/\//g, "-")}`);
  }
}

// S12.3 感知页 file 感官全生命周期：填表接入 → 列表出现 → 停用/启用
// → 移除（确认弹窗）→ 行消失。
async function sensorsCrud() {
  await openIdentity("/sensors");
  const watchDir = mkdtempSync(join(tmpdir(), "uitest-watch-"));
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "文件目录" }).click();
  await page.getByPlaceholder("要观察的目录").fill(watchDir);
  await page.getByPlaceholder("关键词（逗号分隔，可空）").fill("uitest探针");
  await page.getByRole("button", { name: "接入" }).click();
  await page.getByText(watchDir).first().waitFor({ timeout: 8000 });
  const row = page.locator("div.rounded-lg.border", { hasText: watchDir }).first();
  assert(await row.isVisible(), "接入后列表未见新感官");

  // 启停开关（aria-label 含"停用"）。
  const toggle = row.locator('button[aria-label*="停用"]').first();
  await toggle.click();
  await page.getByText("已暂停").first().waitFor({ timeout: 8000 }).catch(() => {});
  // 移除：确认弹窗。弹窗描述含观察路径——先等弹窗退场，再断言
  // 列表空态（否则退场动画期间的残留文本会误判"行仍在"）。
  await row.getByRole("button", { name: "移除" }).click();
  await page.getByText("移除感官").waitFor({ timeout: 5000 });
  await page.getByRole("button", { name: "移除" }).last().click();
  await page.getByText("移除感官").waitFor({ state: "detached", timeout: 8000 });
  await page.getByText("眼睛还是闭着的").waitFor({ timeout: 8000 });
  await shot("S12.3-sensors-crud");
}

// S12.4 webhook（耳）创建：secret 一次性提示语义出现在 UI。
async function webhookSecretOnce() {
  await openIdentity("/sensors");
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "webhook" }).click();
  await page.getByPlaceholder("https://example.com").fill("https://example.com/hook");
  await page.getByRole("button", { name: "接入" }).click();
  await page.waitForTimeout(600);
  const text = await page.locator("body").innerText();
  assert(/只显示这一次|HMAC/.test(text), "webhook 创建后未见一次性密钥提示语义");
  // 清理：移除刚建的 webhook（按含 example.com 的卡片）。
  const row = page.locator("div.rounded-lg.border", { hasText: "example.com" }).first();
  if (await row.isVisible()) {
    await row.getByRole("button", { name: "移除" }).click();
    await page.getByRole("button", { name: "移除" }).last().click();
    await page.waitForTimeout(600);
  }
  await shot("S12.4-webhook");
}

// S12.5 身·触达三态：qa-e2e 未接入（命令指引）；ada 已接入（白名单）。
async function bodySection() {
  await openIdentity("/sensors");
  let text = await page.locator("body").innerText();
  assert(text.includes("身 · 触达"), "感知页缺『身·触达』分区");
  assert(/未接入/.test(text), "qa-e2e 应显示未接入态");
  await page.goto(`${BASE}/i/ada/sensors`, { waitUntil: "networkidle" });
  await page.waitForSelector("a", { timeout: 8000 });
  text = await page.locator("body").innerText();
  if (text.includes("已接入")) {
    assert(/白名单|观察模式/.test(text), "ada 身分区缺授权面详情");
  }
  await shot("S12.5-body");
}

// S12.6 配置页：渲染且不泄露明文凭据（页面文本无 sk- 形态密钥）。
async function configPage() {
  await openIdentity("/config");
  const text = await page.locator("body").innerText();
  assert(!/sk-[A-Za-z0-9]{16,}/.test(text), "配置页疑似泄露明文凭据");
  await shot("S12.6-config");
}

// S12.7 未知路由：干净的 404 页（"请求的页面不存在"），不白屏不崩。
async function notFound() {
  await page.goto(`${BASE}/i/${IDENT}/no-such-tab`, { waitUntil: "networkidle" });
  const text = await page.locator("body").innerText();
  assert(text.includes("404") && text.includes("请求的页面不存在"), `未知路由应显示 404 页，实际：${text.slice(0, 80)}`);
  await shot("S12.7-notfound");
}

const SCENARIOS = { home, navTabs, sensorsCrud, webhookSecretOnce, bodySection, configPage, notFound };

browser = await chromium.launch({ channel: "msedge", headless: true });
ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
page = await ctx.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(String(e).slice(0, 200)));

let failed = 0;
const t0 = Date.now();
for (const [name, fn] of Object.entries(SCENARIOS)) {
  if (ONLY !== "all" && ONLY !== name) continue;
  const s = Date.now();
  try {
    await fn();
    results.push(`SCENARIO ${name} PASS ${Date.now() - s}ms`);
  } catch (e) {
    failed++;
    const reason = String(e?.message ?? e).split("\n")[0].slice(0, 300);
    results.push(`SCENARIO ${name} FAIL ${Date.now() - s}ms ${reason}`);
    try { await shot(`FAIL-${name}`); } catch {}
  }
}
results.push(`PAGEERRORS ${errors.length} ${errors.slice(0, 3).join(" | ")}`);
results.push(`DONE ${results.filter((r) => r.includes(" PASS ")).length} pass, ${failed} fail, ${Date.now() - t0}ms, artifacts=${ART}`);
console.log(results.join("\n"));
writeFileSync(join(ART, "last-run.txt"), results.join("\n") + "\n");
await browser.close();
process.exit(failed > 0 ? 1 : 0);
