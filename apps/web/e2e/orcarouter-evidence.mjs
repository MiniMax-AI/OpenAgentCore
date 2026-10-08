// The headless entry point for the OrcaRouter evidence run. Playwright cannot
// be launched by name here: the packaged Chromium image has no global package
// manager on PATH, so the run starts through the workspace's own Vite and
// @playwright/test by absolute path.
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { spawn } from "node:child_process";

const web = dirname(dirname(fileURLToPath(import.meta.url)));
const playwright = join(web, "node_modules", "@playwright", "test", "cli.js");
const chromium = process.env.AGENTS_E2E_CHROMIUM
  ?? (existsSync("/opt/google/chrome/chrome") ? undefined : existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined);

const env = { ...process.env, OAC_EVIDENCE: "1" };
if (chromium) env.AGENTS_E2E_CHROMIUM = chromium;
const child = spawn(
  process.execPath,
  [playwright, "test", "e2e/orcarouter-evidence.spec.ts", "--config", "playwright.evidence.config.ts"],
  { stdio: "inherit", cwd: web, env },
);
const stop = () => child.kill("SIGTERM");
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
child.on("exit", (code) => process.exit(code ?? 0));
