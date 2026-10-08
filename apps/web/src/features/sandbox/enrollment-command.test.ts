import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { nodeInstallCommand, nodeLogCommand, nodeUninstallCommand } from "./enrollment-command";

describe("sandbox connection and enrollment", () => {
  it("points at the system node journal", () => {
    expect(nodeLogCommand("7f3c2a90-fixture")).toBe("sudo journalctl -u oac-node-7f3c2a90-fixture.service");
    expect(nodeLogCommand("a b")).toBe("sudo journalctl -u 'oac-node-a b.service'");
  });
  // "as root": a root shell runs the sudo command without sudo.
  it.each(["success", "download failure", "checksum mismatch", "installer failure", "as root", "no color", "uppercase proxy", "lowercase proxy", "empty proxy", "root proxy", "unsupported platform", "old python", "uninstall", "killed download"])("executes safely, passes the token only on stdin and cleans private downloads after %s", (scenario) => {
    const parent = join(homedir(), ".oac", "tests");
    mkdirSync(parent, { recursive: true });
    const root = mkdtempSync(join(parent, "node-command-"));
    const bin = join(root, "bin"), temporary = join(root, "tmp");
    mkdirSync(bin); mkdirSync(temporary);
    const payload = "verified installer fixture\n";
    const fixture = join(root, "fixture"), report = join(root, "report.json"), sudoReport = join(root, "sudo.json"), printfReport = join(root, "printf.log");
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
if(!args.includes('http://localhost:8080/api/v1/sandbox-node/install/releases/'+'a'.repeat(40)+'/node-install.pyz')) process.exit(91);
fs.writeFileSync(args[args.indexOf('-o')+1],fs.readFileSync(process.env.FIXTURE));
if(process.env.SCENARIO==='killed download') process.kill(process.ppid,'SIGKILL');`);
    executable("sha256sum", `const fs=require('node:fs'), crypto=require('node:crypto');
const line=fs.readFileSync(0,'utf8').trimEnd(), split=line.indexOf('  ');
if(split!==64) process.exit(2);
const actual=crypto.createHash('sha256').update(fs.readFileSync(line.slice(split+2))).digest('hex');
process.exit(actual===line.slice(0,split)?0:1);`);
    executable("flock", `process.exit(0);`);
    executable("uname", `console.log(process.argv[2]==='-s'?(process.env.SCENARIO==='unsupported platform'?'Darwin':'Linux'):'x86_64');`);
    executable("id", `console.log((process.env.SCENARIO==='as root'||process.env.SCENARIO==='root proxy')?'0':'1000');`);
    // The shell's builtin printf feeds the token; an external one would put it in an argv.
    executable("printf", `require('node:fs').appendFileSync(process.env.PRINTF_REPORT,'called\\n');`);
    executable("sudo", `const fs=require('node:fs'), {spawnSync}=require('node:child_process');
fs.writeFileSync(process.env.SUDO_REPORT,JSON.stringify({args:process.argv.slice(2),env:process.env}));
const args=process.argv.slice(2), keep=args.shift().split('=')[1].split(',');
const env={...process.env}; delete env.NO_COLOR;
for(const key of Object.keys(env)) if(/_proxy$/i.test(key)&&!keep.includes(key)) delete env[key];
process.exit(spawnSync(args[0],args.slice(1),{stdio:'inherit',env}).status ?? 1);`);
    executable("python3", `const fs=require('node:fs');
if(process.argv[2]==='-c') process.exit(process.env.SCENARIO==='old python'?1:0);
const stdin=fs.readFileSync(0,'utf8');
fs.writeFileSync(process.env.REPORT,JSON.stringify({args:process.argv.slice(2),env:process.env,stdin,mode:fs.statSync(process.argv[2]).mode&511}));
process.exit(process.env.SCENARIO==='installer failure'?7:0);`);
    const digest = scenario === "checksum mismatch" ? "0".repeat(64) : createHash("sha256").update(payload).digest("hex");
    const token = "fixture'one-time";
    try {
      const command = scenario === "uninstall" ? nodeUninstallCommand({ sourceUrl: "http://localhost:8080", installationId: "fixture-installation", scriptDigest: digest, sourceCommit: "a".repeat(40) }) : nodeInstallCommand({ token, coreUrl: "http://127.0.0.1:8091", sourceUrl: "http://localhost:8080", provider: "docker", installationId: "fixture-installation", scriptDigest: digest, sourceCommit: "a".repeat(40) });
      const proxy = "http://fixture:private%20password@proxy.example:3128";
      const proxyEnv: Record<string, string | undefined> = { http_proxy: undefined, https_proxy: undefined, no_proxy: undefined, HTTP_PROXY: undefined, HTTPS_PROXY: undefined, NO_PROXY: undefined, ALL_PROXY: undefined };
      if (["uppercase proxy", "root proxy", "empty proxy"].includes(scenario)) Object.assign(proxyEnv, { HTTP_PROXY: proxy, HTTPS_PROXY: proxy, NO_PROXY: "core.example,localhost" });
      if (scenario === "lowercase proxy") Object.assign(proxyEnv, { http_proxy: proxy, https_proxy: proxy, no_proxy: "core.example,localhost", HTTP_PROXY: "http://wrong.invalid" });
      if (scenario === "empty proxy") proxyEnv.http_proxy = "";
      const options = { env: { ...process.env, ...proxyEnv, HOME: root, NO_COLOR: scenario === "no color" ? "" : undefined, PATH: `${bin}:${process.env.PATH}`, TMPDIR: temporary, SCENARIO: scenario, FIXTURE: fixture, REPORT: report, SUDO_REPORT: sudoReport, PRINTF_REPORT: printfReport }, encoding: "utf8" as const };
      let result = spawnSync("sh", ["-c", command], options);
      if (scenario === "killed download") {
        expect(result.signal === "SIGKILL" || result.status === 137).toBe(true);
        expect(readdirSync(join(root, ".oac/node-bootstrap"))).toContain("download.partial");
        result = spawnSync("sh", ["-c", command], { ...options, env: { ...options.env, SCENARIO: "success" } });
      }
      if (["unsupported platform", "old python"].includes(scenario)) {
        expect(result.status).toBe(1);
        expect(result.stdout).not.toContain("Downloading");
        expect(result.stderr).toContain(scenario === "old python" ? "Python 3.9" : "Linux amd64");
        expect(readdirSync(root)).not.toContain("report.json");
        expect(readdirSync(temporary)).toEqual([]);
        return;
      }
      expect(result.status).toBe(scenario === "installer failure" ? 7 : scenario === "download failure" ? 22 : scenario === "checksum mismatch" ? 1 : 0);
      expect(result.stdout).toContain("==> Downloading node installer...");
      expect(result.stdout.includes("==> Verifying node installer...")).toBe(scenario !== "download failure");
      expect(result.stdout + result.stderr).not.toContain(token);
      expect(result.stdout + result.stderr).not.toContain("private%20password");
      expect(result.stdout + result.stderr).not.toContain("\u001b[");
      expect(readdirSync(temporary)).toEqual([]);
      expect(readdirSync(join(root, ".oac/node-bootstrap"))).not.toContain("download.partial");
      expect(readdirSync(root)).not.toContain("printf.log");
      if (scenario === "download failure" || scenario === "checksum mismatch") {
        expect(readdirSync(root)).not.toContain("report.json");
        expect(readdirSync(root)).not.toContain("sudo.json");
        return;
      }
      const invocation = JSON.parse(readFileSync(report, "utf8")) as { args: string[]; env: Record<string, string>; stdin: string; mode: number };
      // The token arrives on stdin alone: in no argument and no environment variable, the installer's or sudo's.
      expect(invocation.env.http_proxy).toBe(["uppercase proxy", "lowercase proxy", "root proxy"].includes(scenario) ? proxy : "");
      expect(invocation.args.join(" ")).not.toContain("private%20password");
      for (const key of ["http_proxy", "https_proxy", "no_proxy"]) {
        const expected = proxyEnv[key] ?? proxyEnv[key.toUpperCase()] ?? "";
        expect(invocation.env[key]).toBe(expected);
        expect(invocation.env[key.toUpperCase()]).toBe(expected);
      }
      expect(invocation.stdin).toBe(scenario === "uninstall" ? "" : `${token}\n`);
      expect(invocation.args.some((arg) => arg.includes("one-time"))).toBe(false);
      expect(Object.values(invocation.env).some((value) => value.includes("one-time"))).toBe(false);
      expect(invocation.args.slice(1)).toEqual(scenario === "uninstall" ? ["--uninstall", "--installation-id", "fixture-installation"] : [...(scenario === "no color" ? ["--no-color"] : []), "--enrollment-token-stdin", "--source-url", "http://localhost:8080", "--core-url", "http://127.0.0.1:8091", "--provider", "docker", "--installation-id", "fixture-installation"]);
      expect(invocation.mode & 0o077).toBe(0);
      if (scenario === "as root" || scenario === "root proxy") {
        expect(readdirSync(root)).not.toContain("sudo.json");
      } else {
        const sudo = JSON.parse(readFileSync(sudoReport, "utf8")) as { args: string[]; env: Record<string, string> };
        expect(sudo.args.slice(0, 2)).toEqual(["--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY", "python3"]);
        expect(sudo.args.join(" ")).not.toContain("private%20password");
        expect(sudo.args.some((arg) => arg.includes("one-time"))).toBe(false);
        expect(Object.values(sudo.env).some((value) => value.includes("one-time"))).toBe(false);
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
});
