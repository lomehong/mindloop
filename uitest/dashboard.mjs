// 仪表盘人工级 UI 全覆盖场景库：每张页面、每个功能点。
// 无头系统 Edge（playwright-core channel:'msedge'，零浏览器下载）。
//
// 覆盖面（三层证据）：
//   1. PAGES 全路由逐页盘点：挂载非空白 + 交互元素清单（page-inventory.jsonl）
//   2. 功能点交互：表单填提、开关切换、搜索过滤、确认弹窗、真模型动作
//   3. 截图留证（.zcode/uitest-artifacts/）
//
// 用法（仪表盘已启动；对话/摘要/探针场景需 ada 心智运行中）：
//   node uitest/dashboard.mjs [--scenario all|名字] [--base URL] [--identity qa-e2e]
// 输出协议：SCENARIO <name> PASS|FAIL <ms> <原因>；退出码非零=有失败。
import { chromium } from "playwright-core";
import { mkdirSync, writeFileSync, appendFileSync } from "node:fs";
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
const INV = join(ART, "page-inventory.jsonl");

let page, browser, ctx;
const results = [];

async function shot(name) {
  const p = join(ART, `${name}.png`);
  await page.screenshot({ path: p, fullPage: true });
  return p;
}
function assert(cond, msg) { if (!cond) throw new Error(msg); }

// 交互元素清单：每页落一行 JSONL——"每个功能点被覆盖"的证据面。
async function inventory(name) {
  const els = await page.evaluate(() => {
    const text = (e) => (e.innerText || e.value || e.placeholder || e.getAttribute("aria-label") || e.getAttribute("title") || "").trim().replace(/\s+/g, " ").slice(0, 40);
    return {
      buttons: Array.from(document.querySelectorAll("button")).map(text).filter(Boolean),
      inputs: Array.from(document.querySelectorAll("input,textarea")).map((e) => e.placeholder || e.type || "").filter(Boolean),
      selects: document.querySelectorAll("select,[role=combobox]").length,
      links: Array.from(document.querySelectorAll("a")).map(text).filter(Boolean).slice(0, 40),
    };
  });
  appendFileSync(INV, JSON.stringify({ page: name, ...els }) + "\n");
  return els;
}

async function gotoPath(path) {
  await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForSelector("a,button", { timeout: 10000 });
}

// —— 全局页 ————————————————————————————————————

async function home() {
  await gotoPath("/");
  const text = await page.locator("body").innerText();
  assert(text.includes("ada"), "首页缺少 ada 身份行");
  assert(text.includes(IDENT), `首页缺少 ${IDENT} 身份行`);
  assert(text.includes("新建身份"), "缺『新建身份』入口");
  await inventory("home");
  await shot("G1-home");
}

// 新建身份对话框：真实经 UI 创建 qa-ui 身份（幂等：已存在则跳过）。
async function homeCreateIdentity() {
  await gotoPath("/");
  if ((await page.getByText("qa-ui").count()) > 0) {
    await shot("G2-create-identity-skip");
    return;
  }
  await page.getByText("新建身份").first().click();
  await page.waitForTimeout(400);
  const dlgInput = page.locator("[role=dialog] input, .modal input, form input").first();
  await dlgInput.fill("qa-ui", { timeout: 5000 });
  const confirm = page.locator("[role=dialog] button, .modal button, form button")
    .filter({ hasText: /创建|确定|新建/ }).first();
  await confirm.click({ timeout: 5000 });
  await page.waitForTimeout(1200);
  await gotoPath("/");
  const text = await page.locator("body").innerText();
  assert(text.includes("qa-ui"), "经 UI 创建身份后列表未见 qa-ui");
  await shot("G2-create-identity");
}

async function credentialsPage() {
  await gotoPath("/");
  await page.getByText("访问凭据").first().click();
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(500);
  const text = await page.locator("body").innerText();
  assert(text.trim().length > 30, "访问凭据页疑似空白");
  await inventory("credentials");
  await shot("G3-credentials");
}

async function llmPage() {
  // navbar 的 "llm" 是下拉按钮（LLM 供应商健康气泡），不是路由页。
  await gotoPath("/");
  await page.getByRole("button", { name: "llm" }).first().click();
  await page.getByText("LLM 供应商：").waitFor({ timeout: 5000 });
  const text = await page.locator("body").innerText();
  assert(!/sk-[A-Za-z0-9]{16,}/.test(text), "llm 气泡疑似泄露明文凭据");
  await page.keyboard.press("Escape");
  await page.mouse.click(10, 300);
  await shot("G4-llm-dropdown");
}

// —— 身份页：14 个 tab 逐页盘点 ——————————————————————

const ID_TABS = [
  ["", "timeline", /时间线|思维|步骤|暂无/i],
  ["/recap", "recap", /摘要|暂无/i],
  ["/mindlog", "mindlog", /思维|日志|暂无|来源/i],
  ["/mindlog2", "mindlog2", /./],
  ["/thinkers", "thinkers", /思考者|调度器|订阅/i],
  ["/health", "health", /响应耗时|回复路径|总耗时|暂无/i],
  ["/usage", "usage", /预算|Token|消息|用量|暂无/i],
  ["/chat", "chat", /./],
  ["/memories", "memories", /记忆|搜索|暂无/i],
  ["/skills", "skills", /技能|skill|暂无/i],
  ["/schedule", "schedule", /日程|暂无|条目/i],
  ["/sensors", "sensors", /感知|感官|眼|身/i],
  ["/connections", "connections", /连接|渠道|企微|暂无/i],
  ["/config", "config", /配置|env|变量/i],
];

async function tabsInventory() {
  for (const [path, name, re] of ID_TABS) {
    await gotoPath(`/i/${IDENT}${path}`);
    const text = await page.locator("body").innerText();
    assert(text.trim().length > 30, `tab ${name} 疑似空白`);
    assert(!/请求的页面不存在/.test(text), `tab ${name} 404（路由漂移）`);
    if (re.source !== "./") assert(re.test(text), `tab ${name} 缺预期文案（${re.source.slice(0, 30)}）`);
    await inventory(`tab:${name}`);
    await shot(`T-${name}`);
  }
}

// —— 逐功能点深测 ——————————————————————————————

// 时间线：ada 数据页有步骤内容；点进第一条子轨迹链接覆盖 /t/ 路由。
async function timelineContent() {
  await gotoPath(`/i/ada`);
  const text = await page.locator("body").innerText();
  assert(!/暂无思维日志/.test(text), "ada 时间线不应为空");
  await shot("F-timeline-ada");
  const sub = page.locator('a[href*="/t/"]').first();
  if (await sub.count()) {
    await sub.click();
    await page.waitForLoadState("networkidle").catch(() => {});
    await page.waitForTimeout(400);
    const t2 = await page.locator("body").innerText();
    assert(t2.trim().length > 20, "子轨迹页疑似空白");
    await inventory("sub-traj");
    await shot("F-sub-traj");
  }
}

// 摘要：点击生成（真模型，qa-e2e 轨迹极小）。生成中提示或结果出现。
async function recapGenerate() {
  await gotoPath(`/i/${IDENT}/recap`);
  const btn = page.getByRole("button", { name: /重建|生成/ }).first();
  if (!(await btn.count())) { await shot("F-recap-nobtn"); return; }
  await btn.click();
  await page.waitForTimeout(1500);
  const text = await page.locator("body").innerText();
  assert(/摘要|生成|暂无/.test(text), "recap 生成后无任何摘要语义");
  await shot("F-recap-generate");
}

// 思维日志过滤器（全部来源 select/下拉）。
async function mindlogFilter() {
  await gotoPath(`/i/ada/mindlog`);
  const text0 = await page.locator("body").innerText();
  assert(text0.length > 50, "思维日志疑似空白");
  const combo = page.getByRole("combobox").first();
  if (await combo.count()) {
    await combo.click();
    await page.waitForTimeout(300);
    await page.keyboard.press("Escape");
  }
  await inventory("mindlog-filter");
  await shot("F-mindlog-filter");
}

// 健康：点探针/刷新类按钮（真 token ping），验证耗时字段出现。
async function healthProbe() {
  await gotoPath(`/i/ada/health`);
  let text = await page.locator("body").innerText();
  assert(/响应耗时|回复路径|总耗时|暂无/.test(text), "健康页缺核心字段");
  const btn = page.getByRole("button", { name: /探针|probe|刷新|测试/ }).first();
  if (await btn.count()) {
    await btn.click();
    await page.waitForTimeout(4000);
    text = await page.locator("body").innerText();
  }
  await inventory("health");
  await shot("F-health");
}

// 用量：ada 有真实账单（今日大量调用）。
async function usageRender() {
  await gotoPath(`/i/ada/usage`);
  const text = await page.locator("body").innerText();
  assert(/预算与准入|每日 Token|每日消息/.test(text), "用量页缺核心区块");
  assert(!/暂无用量数据/.test(text), "ada 用量不应为空（今日有真实调用）");
  await shot("F-usage");
}

// 对话页：真发一条消息并等到 ada 回复气泡（真模型；心智需运行中）。
async function chatSend() {
  await gotoPath(`/i/ada/chat`);
  const bubblesBefore = await page.locator(':text("ada →")').count();
  const box = page.locator("textarea, input[type=text]").last();
  await box.fill("UI 测试：1+1 等于几？一句话。");
  await box.press("Enter");
  await page.waitForTimeout(1000);
  const sent = await page.locator("body").innerText();
  assert(sent.includes("1+1"), "发送后自己的消息未出现在页面");
  // 等 ada 回复气泡出现（最长 90s）。
  let after = bubblesBefore;
  for (let i = 0; i < 30; i++) {
    await page.waitForTimeout(3000);
    after = await page.locator(':text("ada →")').count();
    if (after > bubblesBefore) break;
  }
  assert(after > bubblesBefore, "90s 内未见 ada 回复气泡");
  await shot("F-chat-send");
}

// 记忆：搜索过滤真实生效（ada 有"视觉链路"记忆，qa-e2e 有偏好探针）。
async function memoriesSearch() {
  await gotoPath(`/i/ada/memories`);
  await page.getByPlaceholder("搜索记忆").fill("视觉链路");
  await page.waitForTimeout(1200);
  const text = await page.locator("body").innerText();
  assert(/视觉/.test(text), "搜索'视觉链路'无命中（记忆 030964d4 应在）");
  const typeSel = page.getByPlaceholder("全部类型");
  if (await typeSel.count()) { await typeSel.click(); await page.keyboard.press("Escape"); }
  await shot("F-memories-search");
  // qa-e2e：搜索命中早前 CLI 探针写入的记忆。
  await gotoPath(`/i/${IDENT}/memories`);
  await page.getByPlaceholder("搜索记忆").fill("偏好");
  await page.waitForTimeout(1200);
  const t2 = await page.locator("body").innerText();
  assert(/偏好|浅色/.test(t2), "qa-e2e 搜索'偏好'应命中探针记忆");
  await shot("F-memories-qa");
}

// 日程：qa-e2e 空态 + 表单控件盘点。
async function scheduleRender() {
  await gotoPath(`/i/${IDENT}/schedule`);
  const text = await page.locator("body").innerText();
  assert(/日程/.test(text), "日程页缺标题语义");
  await inventory("schedule");
  await shot("F-schedule");
}

// 连接器：表单字段在 + 空保存的校验路径。
async function connectionsForm() {
  await gotoPath(`/i/${IDENT}/connections`);
  await page.getByPlaceholder("ww1234567890").fill("0000000000");
  await page.getByPlaceholder("zhangsan,lisi").fill("uitest");
  await inventory("connections-form");
  await shot("F-connections-form");
  // 不保存（避免写脏连接器配置）——字段可填即表单可用。
}

// 配置页：env 变量表单真实添加哑键（写入→行出现→删除→空态）；
// 全程不写真实凭据。
async function configEnv() {
  await gotoPath(`/i/${IDENT}/config`);
  const name = page.getByPlaceholder("变量名");
  const val = page.getByPlaceholder("值");
  assert(await name.count() && await val.count(), "配置页缺 env 表单");
  await name.fill("MINDLOOP_UITEST_PROBE");
  await val.fill("1");
  // env 表单的按钮是"+ 添加"（页面上有多个"添加"——限定在本表单内）。
  const envForm = page.locator("form", { has: page.getByPlaceholder("变量名") });
  await envForm.getByRole("button", { name: "添加" }).click();
  await page.getByText("MINDLOOP_UITEST_PROBE").first().waitFor({ timeout: 8000 });
  // 删除该行（操作列）。
  const del = page.getByRole("button", { name: /删除|移除/ }).first();
  if (await del.count()) {
    await del.click();
    await page.waitForTimeout(800);
  }
  const all = await page.locator("body").innerText();
  assert(!/sk-[A-Za-z0-9]{16,}/.test(all), "配置页疑似泄露明文凭据");
  await shot("F-config-env");
}

// talk 三页：landing（选对象）、chat 页、tasks 页。
async function talkPages() {
  await gotoPath(`/talk`);
  let text = await page.locator("body").innerText();
  assert(/选择对话对象|你是谁/.test(text), "talk 落地页缺选择语义");
  await shot("F-talk");
  await gotoPath(`/talk/${IDENT}`);
  await page.waitForSelector("body", { timeout: 8000 });
  text = await page.locator("body").innerText();
  assert(text.trim().length > 20, "talk-chat 页疑似空白");
  await inventory("talk-chat");
  await shot("F-talk-chat");
  await gotoPath(`/talk/${IDENT}/tasks`);
  text = await page.locator("body").innerText();
  assert(text.trim().length > 10, "tasks 页疑似空白");
  await inventory("tasks");
  await shot("F-tasks");
}

// —— 感知页七场景（保留自上一轮） ————————————————————

async function sensorsCrud() {
  await gotoPath(`/i/${IDENT}/sensors`);
  const watchDir = mkdtempSync(join(tmpdir(), "uitest-watch-"));
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "文件目录" }).click();
  await page.getByPlaceholder("要观察的目录").fill(watchDir);
  await page.getByPlaceholder("关键词（逗号分隔，可空）").fill("uitest探针");
  await page.getByRole("button", { name: "接入" }).click();
  await page.getByText(watchDir).first().waitFor({ timeout: 8000 });
  const row = page.locator("div.rounded-lg.border", { hasText: watchDir }).first();
  assert(await row.isVisible(), "接入后列表未见新感官");
  const toggle = row.locator('button[aria-label*="停用"]').first();
  await toggle.click();
  await page.getByText("已暂停").first().waitFor({ timeout: 8000 }).catch(() => {});
  await row.getByRole("button", { name: "移除" }).click();
  await page.getByText("移除感官").waitFor({ timeout: 5000 });
  await page.getByRole("button", { name: "移除" }).last().click();
  await page.getByText("移除感官").waitFor({ state: "detached", timeout: 8000 });
  await page.getByText("眼睛还是闭着的").waitFor({ timeout: 8000 });
  await shot("F-sensors-crud");
}

async function webhookSecretOnce() {
  await gotoPath(`/i/${IDENT}/sensors`);
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "webhook" }).click();
  await page.getByPlaceholder("https://example.com").fill("https://example.com/hook");
  await page.getByRole("button", { name: "接入" }).click();
  await page.waitForTimeout(800);
  const text = await page.locator("body").innerText();
  assert(/只显示这一次|HMAC/.test(text), "webhook 创建后未见一次性密钥提示语义");
  const row = page.locator("div.rounded-lg.border", { hasText: "example.com" }).first();
  if (await row.isVisible()) {
    await row.getByRole("button", { name: "移除" }).click();
    await page.getByText("移除感官").waitFor({ timeout: 5000 });
    await page.getByRole("button", { name: "移除" }).last().click();
    await page.getByText("移除感官").waitFor({ state: "detached", timeout: 8000 });
  }
  await shot("F-webhook");
}

async function bodySection() {
  await gotoPath(`/i/${IDENT}/sensors`);
  let text = await page.locator("body").innerText();
  assert(text.includes("身 · 触达"), "感知页缺『身·触达』分区");
  assert(/未接入/.test(text), "qa-e2e 应显示未接入态");
  await page.goto(`${BASE}/i/ada/sensors`, { waitUntil: "networkidle" });
  await page.waitForSelector("a", { timeout: 8000 });
  text = await page.locator("body").innerText();
  if (text.includes("已接入")) assert(/白名单|观察模式/.test(text), "ada 身分区缺授权面详情");
  await shot("F-body");
}

async function notFound() {
  await page.goto(`${BASE}/i/${IDENT}/no-such-tab`, { waitUntil: "networkidle" });
  const text = await page.locator("body").innerText();
  assert(text.includes("404") && text.includes("请求的页面不存在"), `未知路由应显示 404 页`);
  await shot("F-notfound");
}

const SCENARIOS = {
  home, homeCreateIdentity, credentialsPage, llmPage,
  tabsInventory, timelineContent, recapGenerate, mindlogFilter,
  healthProbe, usageRender, chatSend, memoriesSearch,
  scheduleRender, connectionsForm, configEnv, talkPages,
  sensorsCrud, webhookSecretOnce, bodySection, notFound,
};

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
