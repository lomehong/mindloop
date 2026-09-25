import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { NuqsAdapter } from "nuqs/adapters/react-router/v7";
import { useEffect } from "react";
import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  useLocation,
} from "react-router";
import { toast, Toaster } from "sonner";

import { Navbar } from "~/components/navbar";

import type { Route } from "./+types/root";
import "./app.css";

// 全局查询错误兜底：任何 useQuery 重试耗尽后弹 toast。同一 queryKey
// 复用同一个 toast id——sonner 会原地替换而不是堆叠，轮询中的页面
// 失败不会刷屏。写操作（useMutation）各自带 onError toast，不走这里。
// 首屏主查询另配页面内 QueryErrorBanner（见 home/usage/config 等）。
const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => {
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
          content="#f7f9fb"
        />
        <meta
          name="theme-color"
          media="(prefers-color-scheme: dark)"
          content="#15161d"
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

  useEffect(() => {
    if ("serviceWorker" in navigator) {
      navigator.serviceWorker
        .register("/sw.js", { updateViaCache: "none" })
        .then((reg) => reg.update())
        .catch(() => {
          // 不可安装（如 http 下）——不影响功能。
        });
    }
  }, []);

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
        <NuqsAdapter>
          <div className="min-h-screen flex flex-col">
            {!talkMode && <Navbar />}
            <main
              className={
                talkMode
                  ? "flex flex-1 min-h-screen flex-col"
                  : "flex flex-1 min-h-screen flex-col px-4 pt-4 pb-8 sm:px-6"
              }
            >
              <Outlet />
            </main>
          </div>
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
