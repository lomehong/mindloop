import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import { ThemeProvider } from "next-themes";
import { NuqsAdapter } from "nuqs/adapters/react-router/v7";
import { useEffect } from "react";
import {
  isRouteErrorResponse,
  Link,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  useLocation,
} from "react-router";
import { toast, Toaster } from "sonner";

import { IdentityDossier } from "~/components/identity-dossier";
import { NavRail, ShellControls, ShellDragArea, useRailCollapsed } from "~/components/nav-rail";
import { StatusDock } from "~/components/status-dock";
import { SystemPanel } from "~/components/system-panel";
import { AuthError, HttpError } from "~/lib/api";

import type { Route } from "./+types/root";
import "./app.css";

// 全局查询错误兜底：任何 useQuery 重试耗尽后弹 toast。同一 queryKey
// 复用同一个 toast id——sonner 会原地替换而不是堆叠，轮询中的页面
// 失败不会刷屏。写操作（useMutation）各自带 onError toast，不走这里。
// 首屏主查询另配页面内 QueryErrorBanner（见 home/usage/config 等）。
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: (count, error) => !(error instanceof AuthError) && count < 3 },
  },
  queryCache: new QueryCache({
    onError: (error, query) => {
      if (error instanceof AuthError) return;
      // 404 = 资源不存在：由页面空态如实呈现，不是加载故障，不弹 toast。
      if (error instanceof HttpError && error.status === 404) return;
      const message = error instanceof Error ? error.message : String(error);
      toast.error(`加载失败：${message}`, {
        id: `query:${JSON.stringify(query.queryKey)}`,
      });
    },
  }),
});

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh" suppressHydrationWarning>
      <head>
        <meta charSet="utf-8" />
        <meta
          name="viewport"
          content="width=device-width, initial-scale=1, viewport-fit=cover"
        />
        <link rel="manifest" href="/manifest.webmanifest" />
        <link rel="apple-touch-icon" href="/icons/apple-touch-icon.png" />
        <link
          rel="preload"
          href="/fonts/IBMPlexSans-400.woff2"
          as="font"
          type="font/woff2"
          crossOrigin="anonymous"
        />
        <link
          rel="preload"
          href="/fonts/IBMPlexSans-500.woff2"
          as="font"
          type="font/woff2"
          crossOrigin="anonymous"
        />
        <link
          rel="preload"
          href="/fonts/IBMPlexSans-600.woff2"
          as="font"
          type="font/woff2"
          crossOrigin="anonymous"
        />
        <link
          rel="preload"
          href="/fonts/IBMPlexSerif-400.woff2"
          as="font"
          type="font/woff2"
          crossOrigin="anonymous"
        />
        <link
          rel="preload"
          href="/fonts/GoogleSansCode-VariableFont_wght.ttf"
          as="font"
          type="font/ttf"
          crossOrigin="anonymous"
        />
        <meta name="apple-mobile-web-app-capable" content="yes" />
        <meta
          name="apple-mobile-web-app-status-bar-style"
          content="black-translucent"
        />
        <meta
          name="theme-color"
          media="(prefers-color-scheme: light)"
          content="#EFF0E8"
        />
        <meta
          name="theme-color"
          media="(prefers-color-scheme: dark)"
          content="#121814"
        />
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  const location = useLocation();
  // /talk* 路由是手机优先的 PWA：无导航栏、无页面留白。
  const talkMode = location.pathname.startsWith("/talk");
  // /pet 是桌面宠物：壳把它装进透明置顶小窗，同样无导航栏、无留白，
  // 且不注册 Service Worker（窗体要的是即时新鲜状态，不是离线缓存）。
  const petMode = location.pathname.startsWith("/pet");
  // /i/:id 是工作台形态：左导航栏 + 画布 + 右身份档案（圆角卡）。
  const identityMode = /^\/i\/[^/]+/.test(location.pathname);
  // 桌面壳内（透明圆角窗体）：Web 自绘圆角框架与状态坞，壳只留窗框。
  const inShell =
    typeof window !== "undefined" &&
    !!(window as { __TAURI__?: unknown }).__TAURI__;
  // 左栏宽度（224 展开 / 56 收起）：顶部拖动带的左边界跟随。
  const railCollapsed = useRailCollapsed();

  useEffect(() => {
    document.documentElement.classList.toggle("in-shell", inShell && !petMode);
  }, [inShell, petMode]);

  useEffect(() => {
    if (petMode) return;
    if ("serviceWorker" in navigator) {
      navigator.serviceWorker
        .register("/sw.js", { updateViaCache: "none" })
        .then((reg) => reg.update())
        .catch(() => {
          // 不可安装（如 http 下）——不影响功能。
        });
    }
  }, [petMode]);

  const canvas = <Outlet />;

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
        <NuqsAdapter>
          {petMode ? (
            <main className="flex flex-1 flex-col">{canvas}</main>
          ) : talkMode ? (
            <div className="relative flex h-screen flex-col bg-background">
              {/* 壳内：talk 页无导航栏，窗体栏自绘于此——拖动区铺满顶部，
                  落地页左侧给「← 工作台」出口（PWA 无此出口需求，只壳内
                  渲染），右侧窗控三钮。浏览器/PWA 全部不渲染、不位移。 */}
              {inShell && (
                <>
                  <ShellDragArea className="absolute inset-x-0 top-0 z-[65] h-9" />
                  {/^\/talk\/?$/.test(location.pathname) && (
                    <Link
                      to="/"
                      aria-label="返回工作台"
                      className="absolute left-2 top-2 z-[70] flex h-7 items-center gap-0.5 rounded-md pl-1 pr-2 text-xs text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground"
                    >
                      <ChevronLeft className="size-3.5" />
                      工作台
                    </Link>
                  )}
                  <div className="absolute right-2 top-2 z-[70]">
                    <ShellControls />
                  </div>
                </>
              )}
              <main
                className={`flex min-h-0 flex-1 flex-col${inShell ? " pt-9" : ""}`}
              >
                {canvas}
              </main>
            </div>
          ) : (
            <div className="relative flex h-screen min-w-0 bg-background">
              {/* 窗口右上角控件簇：主题切换 +（壳内）窗控三钮——浮在
                  内容卡上方、脱离导航栏。浏览器里只剩主题钮。 */}
              <div className="absolute right-2 top-2 z-[70]">
                <ShellControls />
              </div>
              {/* 壳内：顶部拖动带——16px 通栏顶边 + 内容区 44px 留白带
                  （左栏顶排 224×32 自行承担）。缺了它整条窗顶几乎无处可拖。 */}
              {inShell && (
                <>
                  <ShellDragArea className="absolute inset-x-0 top-0 z-[65] h-4" />
                  <ShellDragArea className={`absolute right-0 top-0 z-[65] h-11 ${railCollapsed ? "left-14" : "left-56"}`} />
                </>
              )}
              {/* 左侧菜单：通顶（直达窗口上下边缘，不参与圆角） */}
              <NavRail />
              {/* 右侧：内容卡（顶条+内容+状态栏页脚）+ 档案/系统卡 */}
              <div className="flex min-w-0 flex-1 flex-col bg-background p-2 pt-11">
                <div className="flex min-h-0 flex-1 gap-2">
                  <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-2xl border border-border/70 bg-card">
                    <div className="flex-1 overflow-y-auto px-5 pt-4 pb-4 sm:px-6">
                      {canvas}
                    </div>
                    <StatusDock />
                  </main>
                  {identityMode ? <IdentityDossier /> : <SystemPanel />}
                </div>
              </div>
            </div>
          )}
          <Toaster theme="system" style={{ fontFamily: "var(--font-sans)" }} />
        </NuqsAdapter>
      </ThemeProvider>
    </QueryClientProvider>
  );
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let message = "出错了";
  let details = "发生了意外错误。";
  let stack: string | undefined;

  if (isRouteErrorResponse(error)) {
    message = error.status === 404 ? "404" : "错误";
    details =
      error.status === 404
        ? "请求的页面不存在。"
        : error.statusText || details;
  } else if (import.meta.env.DEV && error && error instanceof Error) {
    details = error.message;
    stack = error.stack;
  }

  return (
    <main className="px-4 pt-4 pb-8 sm:px-6">
      <h1 className="mb-4 text-2xl font-bold">{message}</h1>
      <p>{details}</p>
      {stack && (
        <pre className="w-full p-4 overflow-x-auto">
          <code>{stack}</code>
        </pre>
      )}
    </main>
  );
}
