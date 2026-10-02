//! sidecar（内嵌 mindloop.exe）的探测与拉起。
//!
//! 程序名恒为字面量 "mindloop.exe"——Windows CreateProcess 的第一
//! 搜索位就是宿主 exe 所在目录：安装态在安装目录、开发态在
//! target/debug，externalBin 都会被放到那里。不存在任何动态程序路
//! 径进入进程派生接口的通道。

use std::process::Child;

use tauri::Manager;

use crate::job::Job;

/// 壳托管的后端进程集合（attached 时 child/job 均为空）。
#[derive(Default)]
pub struct BackendProc {
    pub child: Option<Child>,
    pub _job: Option<Job>,
    pub port: u16,
    pub attached: bool,
    /// 用户显式要求停止：监督循环看到即收尾，不再自动重启。
    pub user_stopped: bool,
    /// 反复启动失败后的暂停态：保持值守，等待托盘手动启动。
    pub halted: bool,
}

/// 确认内嵌 sidecar 存在（仅用于给出可读的错误信息；真正派生走
/// CreateProcess 的应用目录搜索，程序名是字面量）。
fn sidecar_hint(app: &tauri::AppHandle) -> Result<(), String> {
    let mut checked: Vec<String> = Vec::new();
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let p = dir.join("mindloop.exe");
            if p.is_file() {
                return Ok(());
            }
            checked.push(p.to_string_lossy().into_owned());
        }
    }
    if let Ok(res) = app.path().resource_dir() {
        let p = res.join("mindloop.exe");
        if p.is_file() {
            return Ok(());
        }
        checked.push(p.to_string_lossy().into_owned());
    }
    Err(format!(
        "未找到内嵌的 mindloop.exe（已检查 {}）——请重新运行 build.ps1 打包",
        if checked.is_empty() {
            "无候选位置".into()
        } else {
            checked.join("、")
        }
    ))
}

/// 拉起 `mindloop web`。子进程立即纳入 Job Object——壳退出/崩溃时
/// 整树终止。CREATE_NO_WINDOW：GUI 进程派生控制台程序不能闪黑框。
pub fn spawn_backend(
    app: &tauri::AppHandle,
    port: u16,
) -> Result<(Child, Option<Job>), String> {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;

    sidecar_hint(app)?;
    // resource_dir 带 \\?\ verbatim 前缀——必须还原成普通路径再传给
    // 子进程：Go 侧对 verbatim 路径的 viewer 探测会失效（实测走静默
    // 失败分支）。dunce 在保证不越出长度限制的前提下剥前缀。
    let viewer = dunce::simplified(&app
        .path()
        .resource_dir()
        .map_err(|e| format!("资源目录不可用：{e}"))?
        .join("viewer"))
        .to_path_buf();

    // 子进程 stderr/stdout 落独立文件（debug 构建排障用）：秒死时
    // 能拿到 Go 侧的最后输出或 usage 文本。
    let args: Vec<String> = vec![
        "web".into(),
        "--no-build".into(),
        "--no-open".into(),
        "--port".into(),
        port.to_string(),
        "--viewer-dir".into(),
        viewer.to_string_lossy().into_owned(),
    ];
    let mut cmd = std::process::Command::new("mindloop.exe");
    cmd.args(&args);
    // 排障实验：debug 构建不设 CREATE_NO_WINDOW，验证子进程秒退是否
    // 与无控制台相关。
    if !cfg!(debug_assertions) {
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    if cfg!(debug_assertions) {
        if let Ok(exe) = std::env::current_exe() {
            if let Some(dir) = exe.parent() {
                if let Ok(f) = std::fs::File::create(dir.join("sidecar-stderr.log")) {
                    cmd.stderr(std::process::Stdio::from(f));
                }
                if let Ok(f) = std::fs::File::create(dir.join("sidecar-stdout.log")) {
                    cmd.stdout(std::process::Stdio::from(f));
                }
            }
        }
        eprintln!(
            "desktop: spawn mindloop.exe {} (cwd={:?})",
            args.join(" "),
            std::env::current_dir()
                .map(|p| p.to_string_lossy().into_owned())
                .unwrap_or_default()
        );
    }

    let child = cmd
        .spawn()
        .map_err(|e| format!("启动 mindloop.exe 失败：{e}"))?;

    let job = Job::create();
    if let Some(j) = &job {
        if !j.assign_pid(child.id()) {
            // 纳管失败不阻止运行——只失去"壳崩溃不遗孤"保证。
            eprintln!("desktop: 作业对象纳管失败（降级运行）");
        }
    }
    // Job 句柄必须随子进程一起交给调用方保存：句柄一关闭就触发
    // KillOnJobClose，子进程会被自己的作业对象当场终止。
    Ok((child, job))
}

/// 停止壳拉起的子进程：TerminateProcess 即可——web 无状态，等价于
/// 关掉最后一个浏览器标签页。
pub fn kill_child(st: &crate::AppState) {
    let mut b = st.backend.lock().unwrap();
    if let Some(mut c) = b.child.take() {
        let _ = c.kill();
        let _ = c.wait();
    }
}
