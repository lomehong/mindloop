//! 托盘：状态图标 + 菜单（打开 / 状态 / 停止或启动 / 退出）。
//!
//! 「停止 mindloop」只对壳拉起的子进程生效；直连外部实例（计划任务
//! 服务化）时该项禁用——计划任务的 RestartOnFailure 会立即复活进程，
//! 对它下手是徒劳且误导的。

use tauri::menu::{MenuBuilder, MenuItem, MenuItemBuilder};
use tauri::tray::{TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, Wry};

use crate::show_main;

/// 托盘菜单项句柄（运行中动态改文案/可用性）。
pub struct TrayItems {
    pub status: MenuItem<Wry>,
    pub stop: MenuItem<Wry>,
}

pub fn build(app: &AppHandle) -> tauri::Result<TrayItems> {
    let open = MenuItemBuilder::with_id("open", "打开 mindloop").build(app)?;
    let status = MenuItemBuilder::with_id("status", "状态：连接中…")
        .enabled(false)
        .build(app)?;
    let stop = MenuItemBuilder::with_id("stop", "停止 mindloop web").build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "退出…").build(app)?;

    let menu = MenuBuilder::new(app)
        .items(&[&open, &status])
        .separator()
        .item(&stop)
        .separator()
        .item(&quit)
        .build()?;

    TrayIconBuilder::with_id("main")
        .icon(tauri::include_image!("icons/32x32.png"))
        .menu(&menu)
        .show_menu_on_left_click(false)
        .tooltip("mindloop")
        .on_menu_event(|app, event| match event.id().as_ref() {
            "open" => {
                if let Some(w) = app.get_webview_window("main") {
                    show_main(&w);
                }
            }
            "stop" => crate::toggle_backend(app),
            "quit" => crate::request_quit(app),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            // 双击托盘图标 = 打开主窗口。
            if matches!(event, TrayIconEvent::DoubleClick { .. }) {
                if let Some(w) = tray.app_handle().get_webview_window("main") {
                    show_main(&w);
                }
            }
        })
        .build(app)?;

    Ok(TrayItems { status, stop })
}

/// 按后端状态刷新托盘文案与「停止/启动」项。
pub fn update_status(app: &AppHandle, st: &crate::AppState) {
    let s = st.status.lock().unwrap().clone();
    let guard = st.tray.lock().unwrap();
    let Some(items) = guard.as_ref() else {
        return;
    };

    let status_text = match (s.state.as_str(), s.attached) {
        ("running", true) => format!("状态：已连接现有服务 :{}", s.port),
        ("running", false) => format!("状态：运行中 :{}", s.port),
        ("connecting", _) => format!("状态：连接中 :{}", s.port),
        _ => "状态：已停止".to_string(),
    };
    let _ = items.status.set_text(status_text.as_str());

    // 直连外部实例不可停；壳自己的子进程可停/可再启动。
    if s.attached {
        let _ = items.stop.set_enabled(false);
        let _ = items.stop.set_text("停止 mindloop web");
    } else if s.state == "stopped" {
        let _ = items.stop.set_enabled(true);
        let _ = items.stop.set_text("启动 mindloop web");
    } else {
        let _ = items.stop.set_enabled(true);
        let _ = items.stop.set_text("停止 mindloop web");
    }

    if let Some(tray) = app.tray_by_id("main") {
        let tip = match s.state.as_str() {
            "running" => format!("mindloop 运行中 :{}", s.port),
            "connecting" => "mindloop 连接中…".to_string(),
            _ => "mindloop 已停止".to_string(),
        };
        let _ = tray.set_tooltip(Some(tip.as_str()));
    }
}
