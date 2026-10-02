//! 壳的用户设置：`%APPDATA%/com.mindloop.desktop/config.json`。

use std::path::Path;

#[derive(Clone, serde::Serialize, serde::Deserialize)]
#[serde(default)]
pub struct Config {
    /// 优先探测/绑定的端口（被占则向后找空闲口）。
    pub preferred_port: u16,
    /// 托盘「退出」是否弹确认对话框。
    pub confirm_quit: bool,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            preferred_port: 8080,
            confirm_quit: true,
        }
    }
}

pub fn load(dir: &Path) -> Config {
    let path = dir.join("config.json");
    match std::fs::read(&path) {
        Ok(data) => serde_json::from_slice(&data).unwrap_or_default(),
        Err(_) => Config::default(),
    }
}

pub fn save(dir: &Path, cfg: &Config) {
    let path = dir.join("config.json");
    if let Ok(data) = serde_json::to_vec_pretty(cfg) {
        let _ = std::fs::write(path, data);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults() {
        let c = Config::default();
        assert_eq!(c.preferred_port, 8080);
        assert!(c.confirm_quit);
    }

    #[test]
    fn roundtrip() {
        let dir = std::env::temp_dir().join(format!("mindloop-cfg-test-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut c = Config::default();
        c.confirm_quit = false;
        c.preferred_port = 8090;
        save(&dir, &c);
        let loaded = load(&dir);
        assert_eq!(loaded.confirm_quit, false);
        assert_eq!(loaded.preferred_port, 8090);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn corrupted_file_falls_back_to_default() {
        let dir = std::env::temp_dir().join(format!("mindloop-cfg-bad-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(dir.join("config.json"), b"{broken").unwrap();
        let c = load(&dir);
        assert_eq!(c.preferred_port, 8080);
        std::fs::remove_dir_all(&dir).ok();
    }
}
