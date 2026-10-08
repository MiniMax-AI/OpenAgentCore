// Starts the console's dev server for the acceptance suite from the workspace's
// own Vite, so the suite runs without a global package manager on PATH.
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const web = dirname(dirname(fileURLToPath(import.meta.url)));
const port = process.argv[process.argv.indexOf("--port") + 1];
const child = spawn(
  process.execPath,
  [join(web, "node_modules", "vite", "bin", "vite.js"), "--host", "127.0.0.1", "--mode", "test", "--port", port],
  { stdio: "inherit", cwd: web },
);
const stop = () => child.kill("SIGTERM");
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
child.on("exit", (code) => process.exit(code ?? 0));
