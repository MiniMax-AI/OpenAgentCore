// The configuration the independent OrcaRouter evidence run uses. It is the
// console's acceptance configuration with the evidence spec as its only target
// and no HTML report, so a verification checkout can render the screenshots
// with nothing written into the repository besides the evidence directory.
import { existsSync } from "node:fs";
import { defineConfig } from "@playwright/test";

const fixturePort = Number(process.env.AGENTS_FIXTURE_PORT ?? 18092);
const webPort = Number(process.env.AGENTS_WEB_PORT ?? 4174);
const systemChromium = process.env.AGENTS_E2E_CHROMIUM
  ?? (existsSync("/opt/google/chrome/chrome") ? undefined : existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "orcarouter-evidence.spec.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 7_500 },
  reporter: [["line"]],
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    ...(systemChromium === undefined ? { channel: "chrome" } : { launchOptions: { executablePath: systemChromium, args: ["--no-sandbox"] } }),
    trace: "off",
    video: "off",
  },
  webServer: [
    {
      command: "node e2e/fixture-console.mjs",
      url: `http://127.0.0.1:${fixturePort}/__fixture/health`,
      timeout: 15_000,
      stdout: "pipe",
      stderr: "pipe",
    },
    {
      command: `OAC_WEB_DEV_PROXY_TARGET=http://127.0.0.1:${fixturePort} node e2e/serve-web.mjs --port ${webPort}`,
      url: `http://127.0.0.1:${webPort}`,
      timeout: 30_000,
      stdout: "pipe",
      stderr: "pipe",
    },
  ],
});
