// 全站轮询节奏的单一出处：把散落各路由的隐式毫秒字面量收敛为具名
// 常量，取舍写在注释里。只做收敛，不改任何节奏。
// （timeline 的 mindlog 轮询在 lib/use-mindlog.ts 的 MINDLOG_POLL，
// 复用 api.ts 的 pollWhileLive——live 才轮询。）

// ---- identity/status（live 旗标 + 调度器 PID）----
// thinkers/timeline/chat 这类盯运行状态的主页面——2s。
export const STATUS_ACTIVE_POLL_MS = 2000;
// usage/home/skills/recap/config 等次要页面——5s，对实时性要求低。
export const STATUS_BACKGROUND_POLL_MS = 5000;

// ---- thinkers/调度器状态流 ----
// thinkers 页的主内容就是它——2s。
export const THINKERS_FEED_POLL_MS = 2000;
// chat 页只用它渲染「是否睡眠」横幅——5s，同 status 次要页面。
export const THINKERS_IDLE_POLL_MS = STATUS_BACKGROUND_POLL_MS;
// PWA 等回复时加速到 1s（睡眠横幅跟这条数据流）。
export const THINKERS_AWAITING_POLL_MS = 1000;

// ---- 后台任务进度 ----
// 用量/摘要统计、导出构建进行中——1.5s 快速跟随，完成即回各页常规节奏。
export const JOB_PROGRESS_POLL_MS = 1500;
// usage 常态——读缓存文件很便宜，30s。
export const USAGE_IDLE_POLL_MS = 30_000;
// skills 列表——技能目录变化频率低，10s。
export const SKILLS_POLL_MS = 10_000;
// recap——摘要生成状态，5s。
export const RECAP_POLL_MS = 5000;

// ---- chat 发送后快轮询（lib/use-chat.ts 消费）----
// 发送后 60s 窗口内 700ms 一问，让回复近乎即时；窗口外回到 2s 常规
// （与 status 主页面同值）。
export const CHAT_FAST_POLL_WINDOW_MS = 60_000;
export const CHAT_FAST_POLL_MS = 700;
export const CHAT_IDLE_POLL_MS = STATUS_ACTIVE_POLL_MS;
