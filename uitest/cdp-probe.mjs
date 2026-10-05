import { chromium } from "playwright-core";
const b = await chromium.connectOverCDP("http://127.0.0.1:9223");
const ctx = b.contexts()[0];
const page = ctx.pages().find(p => p.url().startsWith("http://tauri.localhost")) ?? ctx.pages()[0];
console.log("top page:", page.url());
const frame = page.frames().find(f => f.url().includes("127.0.0.1:8080"));
console.log("iframe found:", !!frame, frame?.url());
if (frame) {
  const probe = await frame.evaluate(() => {
    const out = { tauri: typeof window.__TAURI__, internals: typeof window.__TAURI_INTERNALS__ };
    if (window.__TAURI__?.window) {
      const w = window.__TAURI__.window.getCurrentWindow();
      out.label = w.label;
      out.minimize = w.minimize ? "fn" : "missing";
      return w.minimize().then(() => { out.minimizeResult = "ok"; return out; })
        .catch((e) => { out.minimizeResult = "ERR: " + String(e?.message ?? e).slice(0, 160); return out; });
    }
    return out;
  });
  console.log("probe:", JSON.stringify(probe, null, 1));
}
b.close();
