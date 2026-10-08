/// <reference types="vitest/config" />

import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

const repositoryRoot = fileURLToPath(new URL("../..", import.meta.url));

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, repositoryRoot, "");
  const target = env.OAC_WEB_DEV_PROXY_TARGET ?? "http://127.0.0.1:8091";

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    test: {
      include: ["src/**/*.test.{ts,tsx}"],
      setupFiles: ["./src/i18n/test-setup.ts"],
    },
    server: {
      proxy: {
        // The console service's own routes (sign-in and domain settings) and the
        // management surfaces it forwards, as in production.
        "/console": { target, changeOrigin: true },
        "/api/v1": { target, changeOrigin: true },
        "/core/v1": { target, changeOrigin: true },
      },
    },
  };
});
