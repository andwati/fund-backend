import { defineConfig } from "vitest/config";
export default defineConfig({
  resolve: { alias: { "@": "/home/ian/dev/fund/src" } },
  test: { include: ["*.test.ts"], environment: "node" },
});
