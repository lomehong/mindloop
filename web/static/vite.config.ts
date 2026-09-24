import { reactRouter } from "@react-router/dev/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [tailwindcss(), reactRouter(), tsconfigPaths()],
  resolve: {
    // Pin the node build of react-dom's server renderer. Under bun (a
    // node-less container install), "react-dom/server" resolves to
    // server.bun.js, which lacks renderToPipeableStream and breaks the
    // SPA-shell prerender at the end of `react-router build`.
    alias: [{ find: /^react-dom\/server$/, replacement: "react-dom/server.node" }],
  },
});
