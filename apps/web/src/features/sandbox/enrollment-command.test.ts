import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { nodeInstallCommand, nodeLogCommand, nodeUninstallCommand, selfHostedInstallCommand, type NodeInstallMode } from "./enrollment-command";

describe("sandbox connection and enrollment", () => {
  const digest = "a".repeat(64);
  const install = (mode: NodeInstallMode) => nodeInstallCommand({ token: "secret'onetime", coreUrl: "https://core.example", sourceUrl: "https://console.example", provider: "docker", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest, mode });
  it("creates the exact sudo command: a leading space, the checked installer run as root, the token only on stdin", () => {
    expect(install("sudo")).toBe(` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
curl -fsS --max-time 30 --max-filesize 1048576 'https://console.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\\n' '${digest}' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\\n' 'secret'\\''onetime' | $s python3 "$d/node-install.pyz" --enrollment-token-stdin --source-url 'https://console.example' --core-url 'https://core.example' --provider 'docker' --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
  });
  it("creates the exact no-sudo command, which differs only in never calling sudo", () => {
    expect(install("user")).toBe(` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 'https://console.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\\n' '${digest}' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\\n' 'secret'\\''onetime' | python3 "$d/node-install.pyz" --enrollment-token-stdin --source-url 'https://console.example' --core-url 'https://core.example' --provider 'docker' --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
  });
  it("creates the exact uninstall commands, with no token", () => {
    const uninstall = (mode: NodeInstallMode) => nodeUninstallCommand({ sourceUrl: "https://console.example", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest, mode });
    expect(uninstall("sudo")).toBe(` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
curl -fsS --max-time 30 --max-filesize 1048576 'https://console.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\\n' '${digest}' "$d/node-install.pyz" | sha256sum -c --status &&
$s python3 "$d/node-install.pyz" --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
    expect(uninstall("user").split("\n").at(-1)).toBe(`python3 "$d/node-install.pyz" --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
    expect(nodeUninstallCommand({ sourceUrl: "https://console.example", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest, mode: "sudo", force: true }).split("\n").at(-1))
      .toBe(`$s python3 "$d/node-install.pyz" --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f' --force)`);
  });
  it("points at the node's journal in each mode", () => {
    expect(nodeLogCommand("7f3c2a90-fixture", "sudo")).toBe("sudo journalctl -u oac-node-7f3c2a90-fixture.service");
    expect(nodeLogCommand("7f3c2a90-fixture", "user")).toBe("journalctl --user -u oac-node-7f3c2a90-fixture.service");
    expect(nodeLogCommand("a b", "user")).toBe("journalctl --user -u 'oac-node-a b.service'");
  });
  it("creates the exact self-hosted executor install command, every value quoted", () => {
    const command = selfHostedInstallCommand({ publicUrl: "https://core.example", digest: "b".repeat(64), environmentId: "env'1", remoteUrl: "wss://core.example/api/v1/agent-daemon/ws" });
    expect(command).toBe(`(umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/self-hosted-install.pyz' -o "$d/install.pyz" &&
printf '%s  %s\\n' '${"b".repeat(64)}' "$d/install.pyz" | sha256sum -c --status &&
python3 "$d/install.pyz" --source-url 'https://core.example' --environment-id 'env'\\''1' --remote 'wss://core.example/api/v1/agent-daemon/ws')`);
  });
  // "as root": a root shell runs the sudo command without sudo; "no sudo": the no-sudo command.
  it.each(["success", "download failure", "checksum mismatch", "installer failure", "as root", "no sudo"])("executes safely, passes the token only on stdin and cleans private downloads after %s", (scenario) => {
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
fs.writeFileSync(args[args.indexOf('-o')+1],fs.readFileSync(process.env.FIXTURE));`);
    executable("sha256sum", `const fs=require('node:fs'), crypto=require('node:crypto');
const line=fs.readFileSync(0,'utf8').trimEnd(), split=line.indexOf('  ');
if(split!==64) process.exit(2);
const actual=crypto.createHash('sha256').update(fs.readFileSync(line.slice(split+2))).digest('hex');
process.exit(actual===line.slice(0,split)?0:1);`);
    executable("id", `console.log(process.env.SCENARIO==='as root'?'0':'1000');`);
    // The shell's builtin printf feeds the token; an external one would put it in an argv.
    executable("printf", `require('node:fs').appendFileSync(process.env.PRINTF_REPORT,'called\\n');`);
    executable("sudo", `const fs=require('node:fs'), {spawnSync}=require('node:child_process');
fs.writeFileSync(process.env.SUDO_REPORT,JSON.stringify({args:process.argv.slice(2),env:process.env}));
process.exit(spawnSync(process.argv[2],process.argv.slice(3),{stdio:'inherit'}).status ?? 1);`);
    executable("python3", `const fs=require('node:fs');
const stdin=fs.readFileSync(0,'utf8');
fs.writeFileSync(process.env.REPORT,JSON.stringify({args:process.argv.slice(2),env:process.env,stdin,mode:fs.statSync(process.argv[2]).mode&511}));
process.exit(process.env.SCENARIO==='installer failure'?7:0);`);
    const digest = scenario === "checksum mismatch" ? "0".repeat(64) : createHash("sha256").update(payload).digest("hex");
    const token = "fixture'one-time";
    const mode: NodeInstallMode = scenario === "no sudo" ? "user" : "sudo";
    try {
      const command = nodeInstallCommand({ token, coreUrl: "http://127.0.0.1:8091", sourceUrl: "http://localhost:8080", provider: "docker", installationId: "fixture-installation", scriptDigest: digest, mode });
      const result = spawnSync("sh", ["-c", command], { env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, TMPDIR: temporary, SCENARIO: scenario, FIXTURE: fixture, REPORT: report, SUDO_REPORT: sudoReport, PRINTF_REPORT: printfReport }, encoding: "utf8" });
      expect(result.status).toBe(scenario === "installer failure" ? 7 : scenario === "download failure" ? 22 : scenario === "checksum mismatch" ? 1 : 0);
      expect(readdirSync(temporary)).toEqual([]);
      expect(readdirSync(root)).not.toContain("printf.log");
      if (scenario === "download failure" || scenario === "checksum mismatch") {
        expect(readdirSync(root)).not.toContain("report.json");
        expect(readdirSync(root)).not.toContain("sudo.json");
        return;
      }
      const invocation = JSON.parse(readFileSync(report, "utf8")) as { args: string[]; env: Record<string, string>; stdin: string; mode: number };
      // The token arrives on stdin alone: in no argument and no environment variable, the installer's or sudo's.
      expect(invocation.stdin).toBe(`${token}\n`);
      expect(invocation.args.some((arg) => arg.includes("one-time"))).toBe(false);
      expect(Object.values(invocation.env).some((value) => value.includes("one-time"))).toBe(false);
      expect(invocation.args.slice(1)).toEqual(["--enrollment-token-stdin", "--source-url", "http://localhost:8080", "--core-url", "http://127.0.0.1:8091", "--provider", "docker", "--installation-id", "fixture-installation"]);
      expect(invocation.mode & 0o077).toBe(0);
      if (scenario === "as root" || scenario === "no sudo") {
        expect(readdirSync(root)).not.toContain("sudo.json");
      } else {
        const sudo = JSON.parse(readFileSync(sudoReport, "utf8")) as { args: string[]; env: Record<string, string> };
        expect(sudo.args.slice(0, 1)).toEqual(["python3"]);
        expect(sudo.args.some((arg) => arg.includes("one-time"))).toBe(false);
        expect(Object.values(sudo.env).some((value) => value.includes("one-time"))).toBe(false);
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
});
