import { defineConfig } from "@playwright/test";

const fixturePort = Number(process.env.AGENTS_FIXTURE_PORT ?? 18092);
const webPort = Number(process.env.AGENTS_WEB_PORT ?? 4174);
const reuseExistingServer = process.env.AGENTS_REUSE_E2E_SERVERS === "1";

export default defineConfig({
  testDir: "./apps/web/e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 7_500 },
  outputDir: "test-results",
  preserveOutput: "always",
  reporter: [
    ["line"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    channel: "chrome",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },
  webServer: [
    {
      command: "node apps/web/e2e/fixture-console.mjs",
      url: `http://127.0.0.1:${fixturePort}/__fixture/health`,
      reuseExistingServer,
      timeout: 15_000,
      stdout: "pipe",
      stderr: "pipe",
    },
    {
      command: `OAC_WEB_DEV_PROXY_TARGET=http://127.0.0.1:${fixturePort} pnpm --filter @oac/web exec vite --host 127.0.0.1 --mode test --port ${webPort}`,
      url: `http://127.0.0.1:${webPort}`,
      reuseExistingServer,
      timeout: 30_000,
      stdout: "pipe",
      stderr: "pipe",
    },
  ],
});
