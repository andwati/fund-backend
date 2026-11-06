import { defineConfig } from "vitest/config";
export default defineConfig({ test: { include: ["gen*.test.ts"], environment: "node" } });
