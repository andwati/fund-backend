import { defineConfig } from "vitest/config";
export default defineConfig({
  resolve: { alias: { "@": "/home/ian/dev/fund/src" } },
  test: { include: ["gen*.test.ts"], environment: "node" },
});
