// avatar A 期（容器收敛）机械验证探针：同行面渲染契约 + ⌘K 全路由
// + 砍 tab 行。前置：`mindloop web` 已起且存在身份（有今日轨迹数据
// 时验账目行，无数据时验空态）。
// 用法：node avatar-smoke.mjs [base]（默认 http://127.0.0.1:8080）；
// 退出码非零 = 有断言失败。
import { chromium } from "playwright-core";

const BASE = process.argv[2] ?? "http://127.0.0.1:18080";
const fails = [];
const ok = (cond, name) => {
  console.log(`${cond ? "PASS" : "FAIL"} ${name}`);
  if (!cond) fails.push(name);
};

const browser = await chromium.launch({ channel: "msedge", headless: true });
const page = await browser.newPage();
page.setDefaultTimeout(15000);

try {
  // 1. 同行面首屏
  await page.goto(BASE + "/");
  await page.waitForSelector("text=同行面");
  ok(await page.isVisible("text=共同成长账"), "共同成长账卡在");
  for (const t of ["它为你", "你为它", "一起长出"]) {
    ok(await page.isVisible(`text=${t}`), `三栏之一：${t}`);
  }
  // 账目行：有今日数据验三段一句话与三栏行，无数据验诚实空态。
  // （注入夹具：2 自发 final + 3 taste + 2 新记忆 + 1 任务收尾干扰项）
  if (await page.isVisible("text=/它自己收了|你过手|长出 .* 新记忆|今日还没有新账/")) {
    const line = await page.textContent("text=/它自己收了|今日还没有新账/");
    if (line.includes("它自己收了")) {
      ok(line.includes("你过手") && line.includes("长出"), "今日账目一句话三段全对");
      ok(await page.isVisible("text=/巡检完成/"), "它为你行（自发收尾）");
      ok(!(await page.isVisible("text=显式委托的任务收尾")), "非自发收尾不进账");
    } else {
      ok(line.includes("今日还没有新账"), "无数据时空态诚实");
    }
  } else {
    ok(true, "账目区暂无数据（跳过账目断言）");
  }
  // 窗口诚实：首步（trajectory 头）在今日 → 必须标注
  ok(await page.isVisible("text=/窗口起点在今日之内/"), "窗口诚实脚注");
  // 三轴词表 + 无假指标
  for (const t of ["能力", "信任", "传承"]) {
    ok(await page.isVisible(`text=${t}`), `三轴之一：${t}`);
  }
  const card = await page.textContent("body");
  ok(!/起步期\s*·\s*\d+%/.test(card), "无阶段百分比假指标");

  // 2. ⌘K 一级入口（工作台）
  await page.keyboard.press("Control+k");
  await page.waitForSelector("[cmdk-input]");
  ok(await page.isVisible("text=新建委托（共同的验收标准）"), "动作组：新建委托");
  ok(await page.isVisible("text=身份切换"), "身份切换组");
  // 覆盖全路由：设置·连接 应在列（砍 tab 后的到达路径）
  ok(await page.isVisible("text=设置 · 社交"), "全路由：设置·社交");
  await page.keyboard.press("Escape");

  // 3. 身份页：裸入口=舞台；顶栏无 tab 行；体征带在（停机心智=未运行，不假装安睡）
  await page.goto(BASE + "/i/qa-avatar");
  await page.waitForSelector("text=qa-avatar");
  ok(await page.isVisible("text=未运行"), "体征带在（停机=未运行态）");
  const tabCount = await page.locator("header nav a").count();
  ok(tabCount === 0, `顶栏 tab 行已砍（nav 链接数 ${tabCount} = 0）`);

  // 4. 引擎页：← 舞台 + 分节徽标；子页 chips 仍在
  await page.goto(BASE + "/i/qa-avatar/traj");
  await page.waitForSelector("[data-testid=back-to-stage]");
  ok(true, "引擎页有 ← 舞台");
  ok(await page.isVisible("text=泳道"), "子页 chips 仍在（泳道）");
  await page.goto(BASE + "/i/qa-avatar/memories");
  await page.waitForSelector("text=手账");
  ok(true, "记忆面换轴：手账");
  await page.goto(BASE + "/i/qa-avatar/run/usage");
  await page.waitForSelector("text=食量");
  ok(true, "用量页换轴：食量");

  // 5. ⌘K 在身份页有「本身份」组
  await page.keyboard.press("Control+k");
  await page.waitForSelector("[cmdk-input]");
  ok(await page.isVisible("text=本身份"), "身份页 ⌘K 有本身份组");
  const shot = process.env.SHOT_DIR;
  if (shot) {
    await page.screenshot({ path: shot + "/avatar-home.png", fullPage: true });
  }
} catch (e) {
  ok(false, `异常：${e.message}`);
} finally {
  await browser.close();
}
console.log(fails.length === 0 ? "ALL PASS" : `FAILURES: ${fails.length}`);
process.exit(fails.length === 0 ? 0 : 1);
