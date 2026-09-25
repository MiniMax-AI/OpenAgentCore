/// <reference types="vitest/config" />

import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

import {
  loadLocalDockerBackendGuideProfile,
  loadLocalDockerGuideProfile,
} from "./src/lib/docker-guide-config.ts";
import { loadProxyBearerAuth } from "./vite-auth.ts";

const repositoryRoot = fileURLToPath(new URL("../..", import.meta.url));

export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, repositoryRoot, "");
  const target = env.AGENTS_API_PROXY_TARGET ?? "http://127.0.0.1:8091";
  const selfHostedSessionsEnabled = env.AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS === "1";
  const openAIHostedSessionsEnabled = env.AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS === "1";
  const environmentFilesEnabled = env.AGENTS_CORE_WEB_ENVIRONMENT_FILES === "1";
  const localDockerGuide = loadLocalDockerGuideProfile(env);
  const localDockerBackendGuide = loadLocalDockerBackendGuideProfile(env);
  const proxyAuth = command === "serve" && mode !== "test"
    ? loadProxyBearerAuth({
        token: env.AGENTS_API_PROXY_TOKEN,
        tokenFile: env.AGENTS_API_PROXY_TOKEN_FILE,
        rootDir: repositoryRoot,
      })
    : undefined;

  return {
    define: {
      __AGENTS_CORE_WEB_DEV_PROXY_AUTH__: JSON.stringify(Boolean(proxyAuth)),
      __AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS__: JSON.stringify(selfHostedSessionsEnabled),
      __AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__: JSON.stringify(openAIHostedSessionsEnabled),
      __AGENTS_CORE_WEB_ENVIRONMENT_FILES__: JSON.stringify(environmentFilesEnabled),
      __AGENTS_CORE_WEB_DOCKER_GUIDE__: JSON.stringify(localDockerGuide),
      __AGENTS_CORE_WEB_DOCKER_BACKEND_GUIDE__: JSON.stringify(localDockerBackendGuide),
    },
    envDir: repositoryRoot,
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
        // The console service's own routes (sign-in, capability flags) and the
        // management surfaces it forwards, as in production.
        "/console": { target, changeOrigin: true },
        "/node-install": { target, changeOrigin: true },
        "/core/v1/sandbox": { target, changeOrigin: true },
        "/core/v1/admin": { target, changeOrigin: true },
        "/v1": {
          target,
          changeOrigin: true,
          configure(proxy) {
            if (!proxyAuth) return;
            proxy.on("proxyReq", (proxyRequest) => {
              proxyRequest.setHeader("authorization", `Bearer ${proxyAuth.token}`);
            });
          },
        },
      },
    },
  };
});
