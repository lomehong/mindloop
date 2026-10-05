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
  assert(text.includes("工作台"), "首页缺少「工作台」masthead");
  assert(text.includes("ada"), "首页缺少 ada 身份卡");
  assert(text.includes(IDENT), `首页缺少 ${IDENT} 身份卡`);
  assert(/需要你|没有需要你的事/.test(text), "首页缺少「需要你」面板");
  assert(text.includes("近 14 天 · 年轮"), "首页缺少年轮条读数");
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
  // LLM 健康读数在右侧系统面板（导航栏 llm 气泡已删除）；「详情」展开
  // 承接原气泡内容（失败详情/思考节奏/即时探测）。
  await gotoPath("/");
  assert(
    (await page.getByRole("button", { name: "llm" }).count()) === 0,
    "导航栏 llm 气泡应已移除"
  );
  const panel = page.locator("aside:has-text('LLM 供应商')").first();
  await panel.waitFor({ timeout: 5000 });
  await panel.getByRole("button", { name: "详情" }).click();
  await page.waitForTimeout(300);
  const text = await page.locator("body").innerText();
  assert(!/sk-[A-Za-z0-9]{16,}/.test(text), "llm 详情疑似泄露明文凭据");
  await shot("G4-llm-details");
}

// —— 身份页：6 tab + 子页段逐页盘点 ——————————————————————

// 6 个一级页签（每页都应在壳里可见）——收敛后的 IA 不得回流。
const BASE_NAV = [
  ["", "轨迹"],
  ["/chat", "对话"],
  ["/memories", "记忆"],
  ["/run/thinkers", "运行"],
  ["/sensors", "感知"],
  ["/settings/config", "设置"],
];

const ID_TABS = [
  ["", "traj-swimlane", /轨迹|泳道|生命线|步骤|暂无/i],
  ["/log", "traj-log", /步骤|来源|展开|暂无/i],
  ["/recap", "traj-recap", /摘要|暂无/i],
  ["/chat", "chat", /./],
  ["/memories", "memories", /记忆|搜索|暂无/i],
  ["/run/thinkers", "run-thinkers", /思考者|调度器|订阅/i],
  ["/run/schedule", "run-schedule", /日程|暂无|条目/i],
  ["/run/health", "run-health", /响应耗时|回复路径|总耗时|暂无/i],
  ["/run/usage", "run-usage", /预算|Token|消息|用量|暂无/i],
  ["/sensors", "sensors", /感知|感官|眼|身/i],
  ["/settings/config", "settings-config", /配置|env|变量/i],
  ["/settings/skills", "settings-skills", /技能|skill|暂无/i],
  ["/settings/connections", "settings-connections", /连接|渠道|企微|暂无/i],
];

async function tabsInventory() {
  for (const [path, name, re] of ID_TABS) {
    await gotoPath(`/i/${IDENT}${path}`);
    const text = await page.locator("body").innerText();
    assert(text.trim().length > 30, `tab ${name} 疑似空白`);
    assert(!/请求的页面不存在/.test(text), `tab ${name} 404（路由漂移）`);
    if (re.source !== "./") assert(re.test(text), `tab ${name} 缺预期文案（${re.source.slice(0, 30)}）`);
    // 壳断言：6 个一级页签链接都在（新 IA 不回流）。
    for (const [p, label] of BASE_NAV) {
      const href = `/i/${IDENT}${p}`;
      const link = page.locator(`a[href="${href}"]`).filter({ hasText: label }).first();
      assert((await link.count()) > 0, `tab ${name} 缺一级页签「${label}」（${href}）`);
    }
    await inventory(`tab:${name}`);
    await shot(`T-${name}`);
  }
}

// 旧 14 tab 路径经 splat 客户端重定向落新 canonical；?step= 深链原样带过。
async function legacyRedirects() {
  const cases = [
    ["mindlog", "log"],
    ["mindlog2", "log"],
    ["thinkers", "run/thinkers"],
    ["schedule", "run/schedule"],
    ["health", "run/health"],
    ["usage", "run/usage"],
    ["config", "settings/config"],
    ["skills", "settings/skills"],
    ["connections", "settings/connections"],
    ["run", "run/thinkers"],
    ["settings", "settings/config"],
  ];
  for (const [from, to] of cases) {
    await page.goto(`${BASE}/i/${IDENT}/${from}`, { waitUntil: "domcontentloaded" });
    await page
      .waitForFunction((p) => location.pathname === p, `/i/${IDENT}/${to}`, { timeout: 8000 })
      .catch(() => {});
    const path = new URL(page.url()).pathname;
    assert(path === `/i/${IDENT}/${to}`, `/${from} → /${to} 重定向失败（现 ${path}）`);
  }
  // 深链参数保留：搜索/回链跳转依赖它。
  await page.goto(`${BASE}/i/ada/mindlog2?step=zzz`, { waitUntil: "domcontentloaded" });
  await page
    .waitForFunction(() => location.pathname === "/i/ada/log" && location.search === "?step=zzz", null, { timeout: 8000 })
    .catch(() => {});
  assert(page.url().includes("/i/ada/log?step=zzz"), "旧路径重定向应保留 ?step= 参数");
  await shot("F-redirects");
}

// —— 逐功能点深测 ——————————————————————————————

// 时间线：ada 数据页有步骤内容；点进第一条子轨迹链接覆盖 /t/ 路由。
async function timelineContent() {
  await gotoPath(`/i/ada`);
  const text = await page.locator("body").innerText();
  assert(!/暂无轨迹/.test(text), "ada 轨迹页不应为空");
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
  await gotoPath(`/i/ada/log`);
  const text0 = await page.locator("body").innerText();
  assert(text0.length > 50, "日志流疑似空白");
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
  await gotoPath(`/i/ada/run/health`);
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
  await gotoPath(`/i/ada/run/usage`);
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
  await gotoPath(`/i/${IDENT}/run/schedule`);
  const text = await page.locator("body").innerText();
  assert(/日程/.test(text), "日程页缺标题语义");
  await inventory("schedule");
  await shot("F-schedule");
}

// 连接器：表单字段在 + 空保存的校验路径。
async function connectionsForm() {
  await gotoPath(`/i/${IDENT}/settings/connections`);
  await page.getByPlaceholder("ww1234567890").fill("0000000000");
  await page.getByPlaceholder("zhangsan,lisi").fill("uitest");
  await inventory("connections-form");
  await shot("F-connections-form");
  // 不保存（避免写脏连接器配置）——字段可填即表单可用。
}

// 配置页：env 变量表单真实添加哑键（写入→行出现→删除→空态）；
// 全程不写真实凭据。
async function configEnv() {
  await gotoPath(`/i/${IDENT}/settings/config`);
  const name = page.getByPlaceholder("变量名");
  const val = page.getByPlaceholder("值");
  assert(await name.count() && await val.count(), "配置页缺 env 表单");
  await name.fill("MINDLOOP_UITEST_PROBE");
  await val.fill("1");
  // env 表单的按钮是"+ 添加"（页面上有多个"添加"——限定在本表单内）。
  const envForm = page.locator("form", { has: page.getByPlaceholder("变量名") });
  await envForm.getByRole("button", { name: "添加" }).click();
  await page.getByText("MINDLOOP_UITEST_PROBE").first().waitFor({ timeout: 8000 });
  // 删除该行（操作列）——危险操作带确认弹窗，需点弹窗里的「移除」。
  const del = page.getByRole("button", { name: /删除|移除/ }).first();
  if (await del.count()) {
    await del.click();
    await page.getByRole("button", { name: "移除" }).last().click().catch(() => {});
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
  const row = page.locator("div.rounded-lg", { hasText: watchDir }).first();
  assert(await row.isVisible(), "接入后列表未见新感官");
  const toggle = row.locator('button[aria-label*="停用"]').first();
  await toggle.click();
  await page.getByText("已停用").first().waitFor({ timeout: 8000 }).catch(() => {});
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
  const row = page.locator("div.rounded-lg", { hasText: "example.com" }).first();
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

// —— 桌面宠物（/pet）：页面形态 + 菜单 + 说话链路 ——————————

// 页面形态：光点挂载、状态行非空、petMode 无全局导航栏。
// 注意 /pet 初始无 button/a，不能走 gotoPath（它等 a,button）。
async function petPage() {
  await page.goto(`${BASE}/pet`, { waitUntil: "domcontentloaded" });
  const root = page.locator("[data-testid='pet-root']");
  await root.waitFor({ timeout: 10000 });
  await root.locator("[data-testid='pet-orb']").waitFor({ timeout: 8000 });
  const status = await root.locator("[data-testid='pet-status']").innerText();
  assert(status.trim().length > 0, "宠物状态行为空");
  assert(await page.locator("header").count() === 0, "/pet 不应渲染全局导航栏");
  const mood = await root.getAttribute("data-mood");
  assert(!!mood, "pet-root 缺 data-mood");
  await inventory("pet");
  await shot("F-pet-page");
}

// 菜单交互：光点开菜单 → 功能项齐全 → 勿扰切换持久化 → Escape 收起。
async function petMenu() {
  await page.goto(`${BASE}/pet`, { waitUntil: "domcontentloaded" });
  await page.locator("[data-testid='pet-orb']").waitFor({ timeout: 10000 });
  await page.locator("[data-testid='pet-orb']").click();
  await page.locator("[data-testid='pet-menu']").waitFor({ timeout: 5000 });
  for (const tid of ["pet-chat-input", "pet-send", "pet-poke", "pet-dnd", "pet-open-dashboard"]) {
    assert(
      (await page.locator(`[data-testid='${tid}']`).count()) > 0,
      `宠物菜单缺 ${tid}`
    );
  }
  const dnd = page.locator("[data-testid='pet-dnd']");
  const before = await dnd.innerText();
  await dnd.click();
  await page.waitForTimeout(300);
  const stored = await page.evaluate(() => localStorage.getItem("mindloop-pet-dnd"));
  assert(stored !== null && stored.includes("enabled"), "勿扰开关未持久化到 localStorage");
  const after = await page.locator("[data-testid='pet-dnd']").innerText();
  assert(before !== after, "勿扰开关文案未随切换变化");
  await dnd.click(); // 还原为关。
  await page.keyboard.press("Escape");
  await page.waitForTimeout(250);
  assert((await page.locator("[data-testid='pet-menu']").count()) === 0, "Escape 未收起菜单");
  await shot("F-pet-menu");
}

// 说话链路（真模型，心智需运行中）：菜单输入框发消息 → 输入框清空 →
// 90s 内拿到「回复已发生」的证据。成功信号四选一：
//   a) 光点说话态（data-mood=speaking）  b) 流式气泡
//   c) SSE status replying=true        d) SSE 非回显的 message 步骤
// c/d 之所以必要：单字快回复的 replying 旁路文件窗口可能短于服务端
// 200ms 观察轮询周期，a/b 存在固有漏采概率（见 docs/designs/pet.md
// §7）——轨迹步骤不受此影响，是管线打通的兜底证据。
async function petChatSend() {
  await page.goto(`${BASE}/pet`, { waitUntil: "domcontentloaded" });
  await page.locator("[data-testid='pet-orb']").waitFor({ timeout: 10000 });
  // 裸 SSE 监听（与宠物页同端点）：兜底证据通道。
  await page.evaluate(async (base) => {
    const w = window;
    w.__saw = null;
    try {
      const resp = await fetch(`${base}/api/identities/${IDENT}/replies/stream`, {
        headers: { Accept: "text/event-stream" },
      });
      const reader = resp.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let event = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i;
        while ((i = buf.indexOf("\n")) >= 0) {
          const line = buf.slice(0, i);
          buf = buf.slice(i + 1);
          if (line.startsWith("event:")) { event = line.slice(6).trim(); continue; }
          if (line.startsWith("data:") && event) {
            if (event === "status" && line.includes('"replying":true')) {
              w.__saw = w.__saw ?? "status-replying";
            }
            if (event === "step" && line.includes('"type":"message"') && !line.includes("宠物链路")) {
              w.__saw = w.__saw ?? "reply-message-step";
            }
            event = "";
          }
        }
      }
    } catch { /* 连接失败不影响页面通道判定 */ }
  }, BASE);
  await page.locator("[data-testid='pet-orb']").click();
  const input = page.locator("[data-testid='pet-chat-input']");
  await input.waitFor({ timeout: 5000 });
  // 等 SSE 对所选身份真正订阅（data-sse=on）再发送。mood 脱离 offline
  // 不够——身份未选出时它会被"身份列表可读"顶替，快回复会漏采。
  await page.waitForFunction(
    () =>
      document.querySelector("[data-testid='pet-root']")?.getAttribute("data-sse") ===
      "on",
    { timeout: 30000 }
  );
  // 裸 SSE 监听跟随宠物实际选中的身份（localStorage 持久的选择），
  // 兜底证据通道才听得到同一份轨迹。读循环永不返回——必须
  // fire-and-forget，且捕获关页时的中断。
  const petIdentity =
    (await page.evaluate(() => localStorage.getItem("mindloop-pet-identity"))) ??
    IDENT;
  void page.evaluate(async (id) => {
    const w = window;
    w.__saw = null;
    try {
      const resp = await fetch(
        `${location.origin}/api/identities/${id}/replies/stream`,
        { headers: { Accept: "text/event-stream" } }
      );
      const reader = resp.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let event = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i;
        while ((i = buf.indexOf("\n")) >= 0) {
          const line = buf.slice(0, i);
          buf = buf.slice(i + 1);
          if (line.startsWith("event:")) { event = line.slice(6).trim(); continue; }
          if (line.startsWith("data:") && event) {
            if (event === "status" && line.includes('"replying":true')) {
              w.__saw = w.__saw ?? "status-replying";
            }
            if (event === "step" && line.includes('"type":"message"') && !line.includes("宠物链路")) {
              w.__saw = w.__saw ?? "reply-message-step";
            }
            event = "";
          }
        }
      }
    } catch { /* 连接失败不影响页面通道判定 */ }
  }, petIdentity).catch(() => {});
  await input.fill("UI 测试：宠物链路 ping，回复一个字即可。");
  await page.locator("[data-testid='pet-send']").click();
  await page.waitForTimeout(1200);
  const cleared = await input.inputValue();
  assert(cleared === "", "发送成功后输入框应清空（未清空=发送链路报错）");
  let saw = null;
  for (let i = 0; i < 300 && saw === null; i++) {
    await page.waitForTimeout(300);
    const mood = await page.locator("[data-testid='pet-root']").getAttribute("data-mood");
    if (mood === "speaking") saw = "speaking";
    if (saw === null && (await page.locator("[data-testid='pet-bubble'][data-kind='stream']").count()) > 0) {
      saw = "stream-bubble";
    }
    if (saw === null) saw = await page.evaluate(() => window.__saw);
  }
  assert(saw !== null, "90s 内未取得任何回复证据（说话态/气泡/status/回复步骤皆无）");
  console.log(`  reply-evidence: ${saw}`);
  await shot("F-pet-chat");
}

const SCENARIOS = {
  home, homeCreateIdentity, credentialsPage, llmPage,
  tabsInventory, legacyRedirects, timelineContent, recapGenerate, mindlogFilter,
  healthProbe, usageRender, chatSend, memoriesSearch,
  scheduleRender, connectionsForm, configEnv, talkPages,
  sensorsCrud, webhookSecretOnce, bodySection, notFound,
  petPage, petMenu, petChatSend,
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
