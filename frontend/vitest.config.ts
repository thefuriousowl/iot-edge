import { mergeConfig } from "vite";
import { defineConfig } from "vitest/config";

import viteConfig from "./vite.config.ts";

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      coverage: {
        provider: "v8",
        include: ["src/**/*.{ts,tsx}"],
        exclude: [
          "src/**/*.test.{ts,tsx}",
          "src/main.tsx",
          "src/types/**",
          "src/vite-env.d.ts",
        ],
        reporter: ["text", "json-summary"],
        thresholds: {
          statements: 82,
          branches: 76,
          functions: 78,
          lines: 88,
        },
      },
    },
  }),
);
