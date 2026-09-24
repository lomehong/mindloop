// Component tests run under vitest + jsdom, outside the react-router build
// (its vite plugin wants a full app context), so this config carries only
// the path alias the app code relies on.
//
// Run with `bun run test`. The viewer CI job runs this before typecheck and
// the production build.
import tsconfigPaths from "vite-tsconfig-paths";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [tsconfigPaths()],
  test: {
    environment: "jsdom",
    include: ["app/**/*.test.{ts,tsx}"],
  },
});
