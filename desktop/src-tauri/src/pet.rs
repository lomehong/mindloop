//! 宠物窗口：透明置顶小窗承载 web 端 `/pet` 页面（极简光点生命体）。
//!
//! 窗体行为（拖动/点击穿透/位置记忆/隐藏）都在页面 JS 里做
//! （web/static/app/components/pet/pet-app.tsx）；这里只负责三件事：
//! - 创建：无则按 `/pet` 建一个透明、置顶、不进任务栏的小窗；
//! - 显隐切换：托盘「宠物」再点一次 = 隐藏/显示；
//! - 逃生门：页面开启点击穿透后收不到鼠标事件，本函数在显示前一律
//!   `set_ignore_cursor_events(false)`——托盘永远是摸得着的退路。
//!
//! 后端未运行时托盘项禁用（页面加载不出来，开了也是空窗）；
//! 后端中途停止时页面自己退化成 offline 状态，不需要壳操心。

use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};

pub fn toggle(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("pet") {
        let _ = w.set_ignore_cursor_events(false);
        if w.is_visible().unwrap_or(false) {
            let _ = w.hide();
        } else {
            let _ = w.show();
        }
        return;
    }

    let port = app
        .state::<crate::AppState>()
        .status
        .lock()
        .unwrap()
        .port;
    let Ok(url) = tauri::Url::parse(&format!("http://127.0.0.1:{port}/pet")) else {
        return;
    };
    let builder = WebviewWindowBuilder::new(app, "pet", WebviewUrl::External(url))
        .title("mindloop 宠物")
        .inner_size(260.0, 300.0)
        .decorations(false)
        .transparent(true)
        // Windows 上透明窗体默认带一层边框阴影，必须关掉才是真悬浮。
        .shadow(false)
        .always_on_top(true)
        .skip_taskbar(true)
        .resizable(false)
        .focused(false);
    if let Err(e) = builder.build() {
        #[cfg(debug_assertions)]
        eprintln!("desktop: 宠物窗口创建失败 {e}");
        #[cfg(not(debug_assertions))]
        let _ = e;
    }
}
