#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

//! mindloop 桌面壳：无边框窗口承载本地仪表盘 + 托盘常驻 + sidecar 进程托管。
//!
//! 进程模型（探测优先）：启动时先探测已有 mindloop web（含计划任务服务化
//! 实例），在跑则直连；没跑则拉起内嵌 sidecar。壳只管理自己 spawn 的子进程
//! （Job Object 绑定，壳崩溃则子进程随之退出）；mind/connector 仍归计划任务
//! 或仪表盘控制。

use std::path::PathBuf;
use std::sync::Mutex;
use std::time::Duration;

use tauri::{Emitter, Manager};

mod config;
mod health;
mod job;
mod proc;
mod tray;

/// 广播给 chrome 页的后端状态。
#[derive(Clone, serde::Serialize, PartialEq)]
pub struct BackendStatus {
    /// connecting | running | stopped
    pub state: String,
    /// true = 连接的是壳外已有实例（不可停）
    pub attached: bool,
    pub port: u16,
    pub detail: String,
}

pub struct AppState {
    pub status: Mutex<BackendStatus>,
    pub cfg: Mutex<config::Config>,
    pub config_dir: PathBuf,
    pub backend: Mutex<proc::BackendProc>,
    pub tray: Mutex<Option<tray::TrayItems>>,
}

impl AppState {
    fn set_status(&self, app: &tauri::AppHandle, next: BackendStatus) {
        let mut cur = self.status.lock().unwrap();
        if *cur != next {
            *cur = next.clone();
            // debug 构建带控制台：状态流可从 stderr 观察（排障用）。
            #[cfg(debug_assertions)]
            eprintln!(
                "desktop: {} port {} attached {} — {}",
                next.state, next.port, next.attached, next.detail
            );
            let _ = app.emit("backend-status", &next);
        }
    }
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            // 第二实例启动：唤起已有主窗口。
            if let Some(w) = app.get_webview_window("main") {
                show_main(&w);
            }
        }))
        .invoke_handler(tauri::generate_handler![
            win_minimize,
            win_maximize_toggle,
            win_hide,
            shell_state,
            backend_toggle,
            quit_app,
        ])
        .on_window_event(|window, event| {
            // 关窗 = 缩入托盘（与主流桌面应用一致）；真正退出走托盘菜单。
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .setup(|app| {
            let handle = app.handle().clone();

            let config_dir = handle.path().app_config_dir()?;
            std::fs::create_dir_all(&config_dir)?;
            let cfg = config::load(&config_dir);

            let items = tray::build(&handle)?;

            app.manage(AppState {
                status: Mutex::new(BackendStatus {
                    state: "connecting".into(),
                    attached: false,
                    port: cfg.preferred_port,
                    detail: "探测已有实例…".into(),
                }),
                cfg: Mutex::new(cfg),
                config_dir,
                backend: Mutex::new(proc::BackendProc::default()),
                tray: Mutex::new(Some(items)),
            });

            if let Some(w) = handle.get_webview_window("main") {
                #[cfg(windows)]
                round_corners(&w);
                let _ = w.show();
            }

            // 后端监督跑在独立常驻线程（单状态机，见 run_supervisor）。
            std::thread::spawn(move || run_supervisor(handle));
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application");

    app.run(|_app, _event| {});
}

// ———— 后端监督（常驻单状态机）————
//
// 每个 tick（300ms，loopback 探测足够便宜）按序判定：
//   attached        → 只探活并回报
//   user_stopped    → 收尸 + 报停止（保持值守，等待再次启动）
//   halted          → 反复失败后的暂停态（保持值守，等待手动启动）
//   子进程活着      → 探测回报 running / connecting
//   需要拉起        → 连续失败 ≥3 次转 halted，否则 spawn
//
// 唯一线程负责全部状态迁移，「停止后再启动」「启动中收到停止」都
// 不会丢事件。

fn run_supervisor(app: tauri::AppHandle) {
    let st = app.state::<AppState>();
    let cfg = st.cfg.lock().unwrap().clone();

    st.set_status(
        &app,
        BackendStatus {
            state: "connecting".into(),
            attached: false,
            port: cfg.preferred_port,
            detail: "探测已有实例…".into(),
        },
    );

    // 已有实例（计划任务服务化 / 手动起的终端）→ 直连，不可停。
    if let Some(port) = health::find_existing(cfg.preferred_port) {
        {
            let mut b = st.backend.lock().unwrap();
            b.port = port;
            b.attached = true;
        }
        st.set_status(
            &app,
            BackendStatus {
                state: "running".into(),
                attached: true,
                port,
                detail: "已连接现有 mindloop 服务".into(),
            },
        );
        tray::update_status(&app, &st);
    } else {
        let port = health::pick_free_port(cfg.preferred_port);
        st.set_status(
            &app,
            BackendStatus {
                state: "connecting".into(),
                attached: false,
                port,
                detail: format!("准备拉起 mindloop web :{port}"),
            },
        );
    }

    let mut failures: u32 = 0;
    loop {
        std::thread::sleep(Duration::from_millis(300));

        let mut b = st.backend.lock().unwrap();

        if b.attached {
            let ok = health::probe(b.port);
            st.set_status(
                &app,
                BackendStatus {
                    state: if ok { "running".into() } else { "connecting".into() },
                    attached: true,
                    port: b.port,
                    detail: if ok {
                        "已连接现有 mindloop 服务".into()
                    } else {
                        "现有服务未响应…".into()
                    },
                },
            );
            tray::update_status(&app, &st);
            continue;
        }

        if b.user_stopped {
            if let Some(mut c) = b.child.take() {
                let _ = c.kill();
                let _ = c.wait();
            }
            st.set_status(
                &app,
                BackendStatus {
                    state: "stopped".into(),
                    attached: false,
                    port: b.port,
                    detail: "已停止".into(),
                },
            );
            tray::update_status(&app, &st);
            failures = 0;
            continue;
        }

        if b.halted {
            st.set_status(
                &app,
                BackendStatus {
                    state: "stopped".into(),
                    attached: false,
                    port: b.port,
                    detail: "反复启动失败已暂停——托盘「启动 mindloop web」可重试".into(),
                },
            );
            tray::update_status(&app, &st);
            continue;
        }

        // 子进程存活 → 探测回报。
        let exited = match b.child.as_mut() {
            Some(c) => c.try_wait().ok().flatten().is_some(),
            None => true,
        };
        if !exited {
            let port = b.port;
            if health::probe(port) {
                failures = 0;
                st.set_status(
                    &app,
                    BackendStatus {
                        state: "running".into(),
                        attached: false,
                        port,
                        detail: "运行中".into(),
                    },
                );
            } else {
                st.set_status(
                    &app,
                    BackendStatus {
                        state: "connecting".into(),
                        attached: false,
                        port,
                        detail: "等待响应…".into(),
                    },
                );
            }
            tray::update_status(&app, &st);
            continue;
        }

        // 需要拉起（首次 / 意外退出后的自动重启）。
        if b.child.is_some() {
            // 先收尸，拿到退出码便于诊断。
            if let Some(mut c) = b.child.take() {
                let st2 = c.wait();
                #[cfg(debug_assertions)]
                {
                    let code = st2.as_ref().map(|s| s.code());
                    eprintln!("desktop: sidecar 退出码 {:?}（None=被终止/信号）", code);
                }
            }
        }
        failures += 1;
        if failures > 3 {
            b.halted = true;
            tray::update_status(&app, &st);
            continue;
        }
        let port = health::pick_free_port(cfg.preferred_port);
        match proc::spawn_backend(&app, port) {
            Ok((child, job)) => {
                b.child = Some(child);
                b._job = job;
                b.port = port;
                st.set_status(
                    &app,
                    BackendStatus {
                        state: "connecting".into(),
                        attached: false,
                        port,
                        detail: format!("正在启动 mindloop web :{port}"),
                    },
                );
            }
            Err(e) => {
                st.set_status(
                    &app,
                    BackendStatus {
                        state: "stopped".into(),
                        attached: false,
                        port,
                        detail: e,
                    },
                );
            }
        }
        tray::update_status(&app, &st);
    }
}

// ———— 托盘/窗口动作 ————

pub fn show_main(w: &tauri::WebviewWindow) {
    let _ = w.show();
    let _ = w.unminimize();
    let _ = w.set_focus();
}

/// 托盘「停止/启动 mindloop web」：仅对壳拉起的子进程生效。
/// 停止 = 标记 user_stopped（监督循环收尸并报停止）；
/// 启动 = 清除停止/暂停标记（监督循环下一个 tick 重新拉起）。
pub fn toggle_backend(app: &tauri::AppHandle) {
    let st = app.state::<AppState>();
    let mut b = st.backend.lock().unwrap();
    if b.attached {
        return;
    }
    if b.user_stopped || b.halted {
        b.user_stopped = false;
        b.halted = false;
        if let Some(mut c) = b.child.take() {
            let _ = c.kill();
            let _ = c.wait();
        }
    } else {
        b.user_stopped = true;
    }
}

fn do_quit(app: &tauri::AppHandle) {
    let st = app.state::<AppState>();
    {
        let mut b = st.backend.lock().unwrap();
        b.user_stopped = true;
        if let Some(mut c) = b.child.take() {
            let _ = c.kill();
            let _ = c.wait();
        }
    }
    app.exit(0);
}

pub fn request_quit(app: &tauri::AppHandle) {
    let st = app.state::<AppState>();
    let confirm = st.cfg.lock().unwrap().confirm_quit;
    if confirm {
        if let Some(w) = app.get_webview_window("main") {
            show_main(&w);
            let _ = app.emit("quit-request", ());
        }
    } else {
        do_quit(app);
    }
}

// ———— Tauri 命令（chrome 页调用）———

#[tauri::command]
fn win_minimize(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.minimize();
    }
}

#[tauri::command]
fn win_maximize_toggle(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        // v2 无 toggle_maximize：按当前状态二选一。
        let maximized = w.is_maximized().unwrap_or(false);
        if maximized {
            let _ = w.unmaximize();
        } else {
            let _ = w.maximize();
        }
    }
}

#[tauri::command]
fn win_hide(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.hide();
    }
}

#[tauri::command]
fn shell_state(state: tauri::State<AppState>) -> BackendStatus {
    state.status.lock().unwrap().clone()
}

#[tauri::command]
fn backend_toggle(app: tauri::AppHandle) {
    toggle_backend(&app);
}

#[tauri::command]
fn quit_app(app: tauri::AppHandle, remember: bool) {
    if remember {
        let st = app.state::<AppState>();
        {
            let mut cfg = st.cfg.lock().unwrap();
            cfg.confirm_quit = false;
            config::save(&st.config_dir, &cfg);
        }
    }
    do_quit(&app);
}

// ———— Windows 观感 ————

/// Win11：给无边框窗口设置 DWM 圆角（Win10 无此属性，静默忽略）。
#[cfg(windows)]
fn round_corners(w: &tauri::WebviewWindow) {
    use windows::Win32::Foundation::HWND;
    use windows::Win32::Graphics::Dwm::{
        DwmSetWindowAttribute, DWMWA_WINDOW_CORNER_PREFERENCE, DWM_WINDOW_CORNER_PREFERENCE,
        DWMWCP_ROUND,
    };
    if let Ok(h) = w.hwnd() {
        let pref: DWM_WINDOW_CORNER_PREFERENCE = DWMWCP_ROUND;
        unsafe {
            let _ = DwmSetWindowAttribute(
                HWND(h.0 as *mut core::ffi::c_void),
                DWMWA_WINDOW_CORNER_PREFERENCE,
                &pref as *const _ as *const core::ffi::c_void,
                u32::try_from(std::mem::size_of::<DWM_WINDOW_CORNER_PREFERENCE>()).unwrap_or(4),
            );
        }
    }
}

#[cfg(not(windows))]
fn round_corners(_w: &tauri::WebviewWindow) {}
