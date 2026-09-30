import { describe, expect, it, vi } from "vitest";
import { loadEnv, type ConfigEnv } from "vite";

import webConfig from "../../vite.config.ts";

vi.mock("vite", async (importOriginal) => ({
  ...await importOriginal<typeof import("vite")>(),
  loadEnv: vi.fn(),
}));

function configure(env: Record<string, string>, command: ConfigEnv["command"] = "serve") {
  vi.mocked(loadEnv).mockReturnValue(env);
  if (typeof webConfig !== "function") throw new Error("Expected a Vite configuration factory");
  return webConfig({ command, mode: "development" });
}

describe("Web Vite settings boundary", () => {
  it("uses current flags and keeps the proxy address out of browser definitions", async () => {
    const config = await configure({
      OAC_WEB_DEV_PROXY_TARGET: "https://private-host-marker.example",
      OAC_WEB_SELF_HOSTED_SESSIONS: "1",
      OAC_WEB_OPENAI_HOSTED_SESSIONS: "1",
      OAC_WEB_ENVIRONMENT_FILES: "1",
    });
    expect(config.define).toEqual({
      __OAC_WEB_SELF_HOSTED_SESSIONS__: "true",
      __OAC_WEB_OPENAI_HOSTED_SESSIONS__: "true",
      __OAC_WEB_ENVIRONMENT_FILES__: "true",
      __OAC_WEB_DOCKER_GUIDE__: "null",
      __OAC_WEB_DOCKER_BACKEND_GUIDE__: "null",
    });
    expect(Object.keys(config.server?.proxy ?? {})).toEqual(["/console", "/node-install", "/core/v1"]);
    expect(config.server?.proxy?.["/core/v1"]).toEqual({ target: "https://private-host-marker.example", changeOrigin: true });
    expect(JSON.stringify(config.define)).not.toContain("private-");
  });


});
