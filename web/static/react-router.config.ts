import type { Config } from "@react-router/dev/config";

export default {
  // SPA mode - no server-side rendering
  ssr: false,
  // React Router v8 迁移先行（消除 dev 启动的 Future Flag 警告）：
  // 路由模块拆分多 chunk、适配 Vite Environment API、请求直通语义、
  // 数据请求 URL 尾斜杠感知、路由中间件引擎（应用未使用中间件，仅铺设引擎）。
  future: {
    v8_splitRouteModules: true,
    v8_viteEnvironmentApi: true,
    v8_passThroughRequests: true,
    v8_trailingSlashAwareDataRequests: true,
    v8_middleware: true,
  },
} satisfies Config;
