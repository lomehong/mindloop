// Component tests run under vitest + jsdom, outside the react-router build
// (its vite plugin wants a full app context), so this config carries only
// the path alias the app code relies on.
//
// Run with `npm run test`. The viewer CI job (npm ci) runs typecheck, this
// suite, and the production build.
import tsconfigPaths from "vite-tsconfig-paths";
import { defineConfig } from "vitest/config";

// Node 24 的实验性 globalThis.localStorage（无 --localstorage-file 时是个
// 返回 undefined 的 getter）会遮蔽 jsdom 的 window.localStorage，让所有读写
// 本地存储的测试假阴性。经 NODE_OPTIONS 关掉——test worker 进程晚于本文件
// 加载、随环境变量继承该 flag。
process.env.NODE_OPTIONS = `${process.env.NODE_OPTIONS ?? ""} --no-experimental-webstorage`.trim();

export default defineConfig({
  plugins: [tsconfigPaths()],
  test: {
    environment: "jsdom",
    include: ["app/**/*.test.{ts,tsx}"],
  },
});
