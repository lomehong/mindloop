import { Navigate, useLocation } from "react-router";

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "~/components/ui/empty";

// 旧路径 → 新 canonical 的客户端重定向（§6.2 路由迁移表）：
// 书签与 uitest 兜底；搜索参数（如 step 深链）原样带过去。
// 非已知末段（打错的路径）原地示 404——不静默回首页掩饰错误。
const TARGET: Record<string, string> = {
  mindlog: "log",
  mindlog2: "log",
  thinkers: "run/thinkers",
  schedule: "run/schedule",
  health: "run/health",
  usage: "run/usage",
  config: "settings/config",
  skills: "settings/skills",
  connections: "settings/connections",
  run: "run/thinkers",
  settings: "settings/config",
};

export default function LegacyRedirect() {
  const { pathname, search } = useLocation();
  const parts = pathname.split("/").filter(Boolean);
  const seg = parts.at(-1) ?? "";
  const target = TARGET[seg];
  if (!target) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <Empty>
          <EmptyHeader>
            <EmptyTitle>404 · 请求的页面不存在</EmptyTitle>
            <EmptyDescription>
              路径 {pathname} 没有对应页面——检查地址或从上方页签进入。
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    );
  }
  const base = `/${parts.slice(0, -1).join("/")}/`;
  return <Navigate to={`${base}${target}${search}`} replace />;
}
