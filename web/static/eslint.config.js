// ESLint flat config：typescript-eslint recommended + eslint-plugin-react-hooks
// recommended（v7 预设，含 Compiler 系规则）。首跑 --fix 后的遗留违规按
// 影响最小修复，确需豁免的在行内说明理由——不做整文件禁用。
// 只覆盖前端源码；排除构建产物与 react-router typegen 生成物。
import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["build/**", ".react-router/**", "node_modules/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs["recommended-latest"].rules,
      // 采纳期降级：v7 新增的 Compiler 系规则在本仓库有约 25 处既有模式
      // （setState-in-effect 的乐观对账/时钟复位，refs 的渲染期惰性缓存），
      // 重构风险大于收益，先以 warn 门禁暴露，迁移另行安排。
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/refs": "warn",
    },
  },
  {
    // Service worker 是独立的 plain JS 环境，不吃 DOM/Node 全局。
    // （public/ 与 app/ 平级，构建时原样拷贝到站点根。）
    files: ["public/sw.js"],
    languageOptions: {
      globals: {
        self: "readonly",
        addEventListener: "readonly",
        caches: "readonly",
        clients: "readonly",
        fetch: "readonly",
        importScripts: "readonly",
        skipWaiting: "readonly",
        registration: "readonly",
        ExtendableEvent: "readonly",
        FetchEvent: "readonly",
        InstallEvent: "readonly",
        ActivateEvent: "readonly",
        MessageEvent: "readonly",
        NotificationEvent: "readonly",
        PushEvent: "readonly",
        Request: "readonly",
        Response: "readonly",
        URL: "readonly",
        Promise: "readonly",
        console: "readonly",
      },
    },
  },
);
