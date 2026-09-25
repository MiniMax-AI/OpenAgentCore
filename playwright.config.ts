import { defineConfig } from "@playwright/test";
import { homedir } from "node:os";
import { join } from "node:path";

const fixturePort = Number(process.env.AGENTS_FIXTURE_PORT ?? 18611);
const webPort = Number(process.env.AGENTS_WEB_PORT ?? 19619);
const output = process.env.AGENTS_E2E_OUTPUT_DIR ?? join(homedir(), ".parsar", "tests", "console-playwright");

export default defineConfig({
  testDir: "./apps/web/e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 7_500 },
  outputDir: join(output, "results"),
  reporter: [["line"], ["html", { open: "never", outputFolder: join(output, "report") }]],
  use: { baseURL: `http://127.0.0.1:${webPort}`, channel: "chrome", trace: "retain-on-failure", screenshot: "only-on-failure", video: "off" },
  webServer: [
    { command: "node apps/web/e2e/fixture-core.mjs", url: `http://127.0.0.1:${fixturePort}/__fixture/health`, timeout: 15_000, reuseExistingServer: false },
    { command: "node apps/web/e2e/start-console.mjs", url: `http://127.0.0.1:${webPort}/healthz`, timeout: 180_000, reuseExistingServer: false, gracefulShutdown: { signal: "SIGTERM", timeout: 5_000 } },
  ],
});
