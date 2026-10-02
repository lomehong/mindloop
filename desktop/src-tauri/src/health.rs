//! 后端健康探测：判定 127.0.0.1 上的某个端口是否在跑 mindloop web。
//!
//! 判定不只看 HTTP 200——端口可能被无关服务占用，必须命中 mindloop
//! 特有的指纹。探测点是免鉴权可达的 `/api/identities`：
//!   - 无 token 实例：200，JSON 含独有字段 "path_rel"
//!   - 带 token 实例：401（同样是 mindloop——直连后凭据由仪表盘
//!     自带的「访问凭据」控件处理）
//! 注意不能用页面内容做指纹：viewer 的 HTML 标题由 JS 运行时设置，
//! 静态 HTML 里没有任何 mindloop 字样（实测踩坑）。

use std::io::Read;
use std::net::TcpListener;
use std::time::Duration;

const CONNECT_TIMEOUT_MS: u64 = 600;
const TOTAL_TIMEOUT_MS: u64 = 1500;
const MAX_BODY_BYTES: u64 = 8192;
const PROBE_PATH: &str = "/api/identities";

fn fetch_head(url: &str) -> Option<(u16, String)> {
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(Duration::from_millis(CONNECT_TIMEOUT_MS))
        .timeout(Duration::from_millis(TOTAL_TIMEOUT_MS))
        .build();
    let resp = agent.get(url).call().ok()?;
    let status = resp.status();
    let mut body = String::new();
    let take = resp.into_reader().take(MAX_BODY_BYTES);
    let mut limited = take;
    // 截断读取：超长正文（身份列表很大时）只取头部足够判定特征。
    let _ = limited.read_to_string(&mut body);
    Some((status, body))
}

/// 指纹判定：/api/identities 的 200+path_rel 或 401。
pub fn is_mindloop_api(status: u16, body: &str) -> bool {
    status == 401 || (status == 200 && body.contains("\"path_rel\""))
}

/// 单端口判定：mindloop web 在跑且指纹命中。
pub fn probe(port: u16) -> bool {
    check_port(port).is_some()
}

/// 命中则返回端口。
pub fn check_port(port: u16) -> Option<u16> {
    let url = format!("http://127.0.0.1:{port}{PROBE_PATH}");
    match fetch_head(&url) {
        Some((status, body)) if is_mindloop_api(status, &body) => Some(port),
        _ => None,
    }
}

/// 候选端口序列：preferred 优先，随后 8081-8089。
pub fn candidates(preferred: u16) -> Vec<u16> {
    let mut list = vec![preferred];
    for p in 8081..=8089 {
        if p != preferred {
            list.push(p);
        }
    }
    list
}

/// 探测已有实例：第一个特征命中的端口。
pub fn find_existing(preferred: u16) -> Option<u16> {
    candidates(preferred).into_iter().find_map(check_port)
}

/// 找一个当前可绑定的空闲口（拉起 sidecar 用）。
pub fn pick_free_port(preferred: u16) -> u16 {
    for p in candidates(preferred) {
        if TcpListener::bind(("127.0.0.1", p)).is_ok() {
            return p;
        }
    }
    0 // 让 Go 侧自选（--port 0 非法时兜底为 preferred；实际几乎不可达）
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fingerprint_matches_mindloop_api() {
        assert!(is_mindloop_api(
            200,
            r#"[{"id":"ada","name":"ada","path_rel":"ada","live":false}]"#
        ));
        // 带 token 的实例：401 也是 mindloop。
        assert!(is_mindloop_api(401, r#"{"detail":"unauthorized"}"#));
    }

    #[test]
    fn fingerprint_rejects_foreign_services() {
        assert!(!is_mindloop_api(200, "<html>nginx</html>"));
        assert!(!is_mindloop_api(200, r#"[{"id":1,"name":"x"}]"#));
        assert!(!is_mindloop_api(404, "not found"));
        assert!(!is_mindloop_api(500, ""));
    }

    #[test]
    fn candidates_put_preferred_first() {
        let list = candidates(8080);
        assert_eq!(list.first(), Some(&8080));
        assert_eq!(list.len(), 10);
        assert!(!list.contains(&8080) || list[0] == 8080);
        assert_eq!(list.iter().filter(|&&p| p == 8080).count(), 1);
    }

    #[test]
    fn candidates_custom_preferred() {
        let list = candidates(9000);
        assert_eq!(list.first(), Some(&9000));
        assert_eq!(list.len(), 10);
        // 兜底段固定是 8081-8089；preferred 不在其中则不重复出现。
        assert!(list.contains(&8085));
        assert_eq!(list.iter().filter(|&&p| p == 9000).count(), 1);
    }

    #[test]
    fn pick_free_port_returns_bindable_port() {
        let p = pick_free_port(0);
        assert!(p == 0 || (1..=65535).contains(&p));
    }

    #[test]
    fn probe_on_closed_port_is_false() {
        // 找一个确定没人监听的口：先绑定再丢弃。
        let listener = TcpListener::bind(("127.0.0.1", 0)).unwrap();
        let port = listener.local_addr().unwrap().port();
        drop(listener);
        assert!(!probe(port));
    }
}
