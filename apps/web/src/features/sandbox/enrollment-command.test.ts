import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { nodeInstallCommand } from "./enrollment-command";

describe("sandbox connection and enrollment", () => {
  it("creates a compact verified installer command with no credential argument or redirect following", () => {
    const command = nodeInstallCommand("secret'onetime", "https://core.example", "https://console.example", "docker", "installation", "a".repeat(64));
    expect(command).toContain("PARSAR_NODE_ENROLLMENT_TOKEN='secret'\\''onetime' python3");
    expect(command).toContain("https://console.example/node-install/node_install.py");
    expect(command).toContain("sha256sum -c --status &&");
    expect(command).toContain("--max-time 30 --max-filesize 1048576");
    expect(command).not.toMatch(/--location| -L/);
    expect(command).not.toContain("--insecure");
    expect(command).toContain("--source-url 'https://console.example' --core-url 'https://core.example' --provider 'docker' --installation-id 'installation'");
    expect(command).not.toContain("--enrollment-token");
    expect(command.split("\n")).toHaveLength(4);
  });
  it.each(["success", "download failure", "checksum mismatch", "installer failure"])("executes safely and cleans private downloads after %s", (scenario) => {
    const parent = join(homedir(), ".parsar", "tests");
    mkdirSync(parent, { recursive: true });
    const root = mkdtempSync(join(parent, "node-command-"));
    const bin = join(root, "bin"), temporary = join(root, "tmp");
    mkdirSync(bin); mkdirSync(temporary);
    const payload = "verified installer fixture\n";
    const fixture = join(root, "fixture"), report = join(root, "report.json");
    writeFileSync(fixture, payload);
    function executable(name: string, code: string) {
      const path = join(bin, name);
      writeFileSync(path, `#!${process.execPath}\n${code}`);
      chmodSync(path, 0o700);
    }
    executable("curl", `const fs=require('node:fs');
if(process.env.SCENARIO==='download failure') process.exit(22);
const args=process.argv.slice(2);
if(args.includes('-L')||args.includes('--location')||args.includes('--insecure')) process.exit(90);
fs.writeFileSync(args[args.indexOf('-o')+1],fs.readFileSync(process.env.FIXTURE));`);
    executable("sha256sum", `const fs=require('node:fs'), crypto=require('node:crypto');
const line=fs.readFileSync(0,'utf8').trimEnd(), split=line.indexOf('  ');
if(split!==64) process.exit(2);
const actual=crypto.createHash('sha256').update(fs.readFileSync(line.slice(split+2))).digest('hex');
process.exit(actual===line.slice(0,split)?0:1);`);
    executable("python3", `const fs=require('node:fs');
fs.writeFileSync(process.env.REPORT,JSON.stringify({args:process.argv.slice(2),token:process.env.PARSAR_NODE_ENROLLMENT_TOKEN,mode:fs.statSync(process.argv[2]).mode&511}));
process.exit(process.env.SCENARIO==='installer failure'?7:0);`);
    const digest = scenario === "checksum mismatch" ? "0".repeat(64) : createHash("sha256").update(payload).digest("hex");
    const token = "fixture'one-time";
    try {
      const command = nodeInstallCommand(token, "http://127.0.0.1:8091", "http://localhost:8080", "docker", "fixture-installation", digest);
      const result = spawnSync("sh", ["-c", command], { env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, TMPDIR: temporary, SCENARIO: scenario, FIXTURE: fixture, REPORT: report }, encoding: "utf8" });
      expect(result.status).toBe(scenario === "success" ? 0 : scenario === "installer failure" ? 7 : scenario === "download failure" ? 22 : 1);
      expect(readdirSync(temporary)).toEqual([]);
      if (scenario === "success" || scenario === "installer failure") {
        const invocation = JSON.parse(readFileSync(report, "utf8")) as { args: string[]; token: string; mode: number };
        expect(invocation.token).toBe(token);
        expect(invocation.args).not.toContain(token);
        expect(invocation.args.slice(1)).toEqual(["--source-url", "http://localhost:8080", "--core-url", "http://127.0.0.1:8091", "--provider", "docker", "--installation-id", "fixture-installation"]);
        expect(invocation.mode & 0o077).toBe(0);
      } else {
        expect(readdirSync(root)).not.toContain("report.json");
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
});
