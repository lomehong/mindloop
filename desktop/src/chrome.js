// mindloop 桌面壳 chrome 逻辑：标题栏控制、后端状态渲染、退出流程。
// 依赖 withGlobalTauri 暴露的 window.__TAURI__（Tauri 2 全局 API）。

const { invoke } = window.__TAURI__.core;
const { listen } = window.__TAURI__.event;

const els = {
  dot: document.getElementById("status-dot"),
  statusText: document.getElementById("status-text"),
  boot: document.getElementById("boot"),
  bootText: document.getElementById("boot-text"),
  bootDetail: document.getElementById("boot-detail"),
  retry: document.getElementById("btn-retry"),
  frame: document.getElementById("app-frame"),
  dialog: document.getElementById("quit-dialog"),
  quitScope: document.getElementById("quit-scope"),
  quitRemember: document.getElementById("quit-remember"),
};

let framePort = 0; // iframe 已指向的端口（0 = 未指向）

function applyStatus(s) {
  els.dot.className = `dot ${s.state}`;

  const text =
    s.state === "running"
      ? s.attached
        ? `已连接现有服务 :${s.port}`
        : `运行中 :${s.port}`
      : s.state === "connecting"
        ? "连接中…"
        : "已停止";
  els.statusText.textContent = text;

  // 后端就绪且 iframe 尚未指向（或端口变了）→ 装载仪表盘。
  if (s.state === "running" && framePort !== s.port) {
    framePort = s.port;
    els.frame.src = `http://127.0.0.1:${s.port}/`;
  }

  // boot 遮罩：仅在未就绪时可见；stopped 时给出重试入口。
  const booted = s.state === "running";
  els.boot.hidden = booted;
  els.frame.hidden = !booted;
  els.retry.hidden = s.state !== "stopped";
  els.bootText.textContent =
    s.state === "connecting" ? "正在连接 mindloop…" : "mindloop 已停止";
  els.bootDetail.textContent = s.detail || "";
}

async function refresh() {
  try {
    applyStatus(await invoke("shell_state"));
  } catch (e) {
    console.error("shell_state 失败", e);
  }
}

// ———— 标题栏按钮 ————

document.getElementById("btn-min").addEventListener("click", () =>
  invoke("win_minimize").catch(() => {})
);
document.getElementById("btn-max").addEventListener("click", () =>
  invoke("win_maximize_toggle").catch(() => {})
);
document.getElementById("btn-close").addEventListener("click", () =>
  invoke("win_hide").catch(() => {})
);
els.retry.addEventListener("click", () =>
  invoke("backend_toggle").catch(() => {})
);

// ———— 退出流程 ————

function openQuitDialog(s) {
  els.quitScope.textContent = s.attached
    ? "mindloop 由计划任务等外部方式管理，退出壳不影响它。"
    : "点击「停止并退出」会一并停止由壳拉起的 mindloop web。";
  els.dialog.showModal();
}

document.getElementById("quit-cancel").addEventListener("click", () => {
  els.dialog.close();
});

document.getElementById("quit-confirm").addEventListener("click", () => {
  const remember = els.quitRemember.checked;
  els.dialog.close();
  invoke("quit_app", { remember }).catch(() => {});
});

// ———— 事件 ————

listen("backend-status", (e) => applyStatus(e.payload)).catch(() => {});
listen("quit-request", async () => {
  const s = await invoke("shell_state");
  openQuitDialog(s);
}).catch(() => {});

refresh();
