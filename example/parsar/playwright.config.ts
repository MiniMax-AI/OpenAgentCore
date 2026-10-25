import { defineConfig } from "@playwright/test";
import { homedir } from "node:os";
import { join } from "node:path";

const appPort = process.env.OAC_EXAMPLE_PORT || "18180";

export default defineConfig({
  testDir: "tests",
  testMatch: "*.spec.ts",
  workers: 1,
  timeout: 60_000,
  outputDir: join(
    process.env.OAC_DEV_HOME || join(homedir(), ".oac"),
    "tests",
    "parsar-example",
  ),
  reporter: "line",
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    reducedMotion: "reduce",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "node tests/fixture.mjs",
      url: "http://127.0.0.1:18181/health",
      reuseExistingServer: false,
    },
    {
      command: "node server.mjs --dev",
      url: `http://127.0.0.1:${appPort}`,
      env: {
        OPENAI_BASE_URL: "http://127.0.0.1:18181/v1",
        OPENAI_API_KEY: "fixture-project-key",
        OAC_EXAMPLE_PORT: appPort,
        OAC_EXAMPLE_DATA_DIR: join(
          homedir(),
          ".oac",
          "tests",
          "parsar-example",
          "fixture-store",
        ),
      },
      reuseExistingServer: false,
    },
  ],
});
