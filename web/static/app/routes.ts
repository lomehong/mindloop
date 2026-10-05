import { type RouteConfig, index, route } from "@react-router/dev/routes";

// 7 tab 收敛后的路由表（docs/designs/ui-language.md §6）：轨迹（泳道/日志流/
// 分集）· 对话 · 任务 · 记忆 · 运行（思考者/日程/健康/用量）· 感知 · 设置
// （配置/技能/连接）。旧路径经 redirect.tsx 客户端重定向兜底。
export default [
  index("routes/home.tsx"),
  route("talk", "routes/talk.tsx"),
  route("talk/:identityId", "routes/talk-chat.tsx"),
  route("talk/:identityId/tasks", "routes/tasks.tsx"),
  route("i/:identityId", "routes/identity-shell.tsx", [
    index("routes/timeline.tsx"),
    route("log", "routes/log.tsx"),
    route("recap", "routes/recap.tsx"),
    route("chat", "routes/chat.tsx"),
    route("tasks", "routes/identity-tasks.tsx"),
    route("memories", "routes/memories.tsx"),
    route("sensors", "routes/sensors.tsx"),
    route("run/thinkers", "routes/thinkers.tsx"),
    route("run/schedule", "routes/schedule.tsx"),
    route("run/health", "routes/health.tsx"),
    route("run/usage", "routes/usage.tsx"),
    route("settings/config", "routes/config.tsx"),
    route("settings/skills", "routes/skills.tsx"),
    route("settings/connections", "routes/connections.tsx"),
    route("t/:trajId", "routes/sub-traj.tsx"),
    // 旧 14 tab 路径 + 裸 run/settings（书签兜底）；React Router 不允许
    // 同一模块挂多个路由，故收成一条 splat，由 redirect.tsx 解析末段。
    route("*", "routes/redirect.tsx"),
  ]),
  route("pet", "routes/pet.tsx"),
] satisfies RouteConfig;
