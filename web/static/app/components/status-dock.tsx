import { useQuery } from "@tanstack/react-query";

import { fetchConfig } from "~/lib/api";

// Trellis 状态坞：壳底部 26px 常驻读数条（随透明圆角壳自绘，替代
// 旧 chrome 状态栏）。连接态的语义是"仪表盘自身 API 可达性"——能
// 渲染出这条bar，后端就活着；fetch 失败只可能是瞬断，显示连接中。
export function StatusDock() {
  const { data: config, isError } = useQuery({
    queryKey: ["status-dock-config"],
    queryFn: fetchConfig,
    refetchInterval: 5000,
    retry: false,
    staleTime: 4000,
  });
  const port = typeof window !== "undefined" ? window.location.port : "";
  const up = !isError && !!config;
  const version = config?.git_commit ? config.git_commit.slice(0, 7) : "";

  return (
    <footer className="flex h-[30px] shrink-0 items-center gap-3 border-t border-border bg-secondary/40 px-4 font-mono text-[11px] text-muted-foreground">
      <span
        aria-hidden
        className={`inline-block h-1.5 w-1.5 rounded-full ${up ? "bg-primary" : "bg-resin"}`}
      />
      <span>{up ? `运行中 :${port}` : "连接中…"}</span>
      <span className="opacity-70">本地</span>
      {version && <span>{version}</span>}
      <span className="flex-1" />
      <span>mindloop 工作台</span>
    </footer>
  );
}
