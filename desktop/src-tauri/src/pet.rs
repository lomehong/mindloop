//! 宠物窗口：透明置顶小窗承载 web 端 `/pet` 页面（极简光点生命体）。
//!
//! 形态跟随主窗（互斥：同一时刻画面里只有一只生物）：
//! - 主窗可见 → 悬浮窗收起（宠物以「停靠」形态活在仪表盘右栏卡片顶部）；
//! - 主窗隐藏/最小化 → 悬浮窗回到桌面（用户开关开启时）。
//! 「开启」= 托盘「宠物」勾选 / 启动参数 `--pet`；宠物菜单「隐藏」即关闭。
//!
//! 窗体行为（拖动/点击穿透/位置记忆/隐藏）都在页面 JS 里做
//! （web/static/app/components/pet/pet-app.tsx）；这里只负责：
//! - 创建：无则按 `/pet` 建一个透明、置顶、不进任务栏的小窗；
//! - 形态对账：`sync()` 由监督循环每 tick 调用（主窗现形时另由 show_main
//!   即时收起，别让两只同屏）；首次建窗等后端就绪（页面加载不出来时开了
//!   也是空窗）；
//! - 逃生门：显示前一律 `set_ignore_cursor_events(false)`——托盘永远是
//!   摸得着的退路。

use std::sync::atomic::Ordering;

use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};

use crate::AppState;

/// 开/关桌面宠物（托盘「宠物」、`--pet`、宠物菜单「隐藏」的唯一入口）。
/// 开启时把主窗收进托盘：主窗可见时宠物本应停靠，用户点「宠物」要的是
/// 桌面上那只——立刻收窗，开与不开一眼可见。
pub fn set_enabled(app: &AppHandle, on: bool) {
    let st = app.state::<AppState>();
    st.pet_enabled.store(on, Ordering::Relaxed);
    set_tray_checked(app, on);
    if on {
        if let Some(w) = app.get_webview_window("main") {
            if w.is_visible().unwrap_or(false) {
                let _ = w.hide();
            }
        }
        // 刚收主窗（或本就无窗）：按「主窗不可见」对账。
        sync_with_main(app, false);
    } else {
        sync_with_main(app, true);
    }
}

fn set_tray_checked(app: &AppHandle, on: bool) {
    let st = app.state::<AppState>();
    let guard = st.tray.lock().unwrap();
    if let Some(items) = guard.as_ref() {
        let _ = items.pet.set_checked(on);
    }
}

/// 读主窗现状再对账（监督循环每拍、窗口 Resized 事件用；稳态下读数可靠）。
pub fn sync(app: &AppHandle) {
    sync_with_main(app, main_shown(app));
}

/// 形态对账：悬浮窗可见 ⟺ 开关开启 && 主窗不可见（隐藏或最小化）。
/// 明示 main_shown 而不是总去回读：show/hide 的派发是异步落地的，
/// 紧接着的回读会拿到旧值——调用方刚收/放了主窗，自己知道意图。
pub fn sync_with_main(app: &AppHandle, main_shown: bool) {
    let st = app.state::<AppState>();
    let enabled = st.pet_enabled.load(Ordering::Relaxed);
    let should_float = enabled && !main_shown;
    let win = app.get_webview_window("pet");

    match (should_float, win) {
        (true, Some(w)) => {
            if !w.is_visible().unwrap_or(false) {
                // 页面可能处在点击穿透态，显示前强制解除（托盘的逃生门）。
                let _ = w.set_ignore_cursor_events(false);
                let _ = w.show();
            }
        }
        (false, Some(w)) => {
            if w.is_visible().unwrap_or(false) {
                let _ = w.hide();
            }
        }
        (true, None) => {
            // 建窗前等后端就绪：/pet 加载不出来时开了也是空窗。
            if st.status.lock().unwrap().state == "running" {
                create(app);
            }
        }
        (false, None) => {}
    }
}

fn main_shown(app: &AppHandle) -> bool {
    app.get_webview_window("main")
        .map(|w| w.is_visible().unwrap_or(false) && !w.is_minimized().unwrap_or(false))
        .unwrap_or(false)
}

fn create(app: &AppHandle) {
    let port = app.state::<AppState>().status.lock().unwrap().port;
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
