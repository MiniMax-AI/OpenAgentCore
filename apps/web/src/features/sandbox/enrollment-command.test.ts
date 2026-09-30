import { describe, expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { nodeInstallCommand, nodeLogCommand, nodeUninstallCommand } from "./enrollment-command";

describe("sandbox connection and enrollment", () => {
  const digest = "a".repeat(64);
  const install = () => nodeInstallCommand({ token: "secret'onetime", coreUrl: "https://core.example", sourceUrl: "https://console.example", provider: "docker", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest });
  it("creates the exact sudo command: a leading space, the checked installer run as root, the token only on stdin", () => {
    expect(install()).toBe(` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
export http_proxy="\${http_proxy-\${HTTP_PROXY-}}" https_proxy="\${https_proxy-\${HTTPS_PROXY-}}" no_proxy="\${no_proxy-\${NO_PROXY-}}"
export HTTP_PROXY="$http_proxy" HTTPS_PROXY="$https_proxy" NO_PROXY="$no_proxy"
printf '\\n==> Downloading node installer...\\n' &&
curl -fs --max-time 30 --max-filesize 1048576 'https://console.example/node-install/node-install.pyz' -o "$d/node-install.pyz" || { c=$?; printf 'Cannot download node installer; check the console URL, TLS and proxy settings.\\n' >&2; exit "$c"; }
printf '==> Verifying node installer...\\n' &&
printf '%s  %s\\n' '${digest}' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\\n' 'secret'\\''onetime' | $s \${s:+--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY} python3 "$d/node-install.pyz" \${NO_COLOR+--no-color} --enrollment-token-stdin --source-url 'https://console.example' --core-url 'https://core.example' --provider 'docker' --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
  });
  it("creates the exact uninstall commands, with no token", () => {
    const uninstall = () => nodeUninstallCommand({ sourceUrl: "https://console.example", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest });
    expect(uninstall()).toBe(` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
export http_proxy="\${http_proxy-\${HTTP_PROXY-}}" https_proxy="\${https_proxy-\${HTTPS_PROXY-}}" no_proxy="\${no_proxy-\${NO_PROXY-}}"
export HTTP_PROXY="$http_proxy" HTTPS_PROXY="$https_proxy" NO_PROXY="$no_proxy"
printf '\\n==> Downloading node installer...\\n' &&
curl -fs --max-time 30 --max-filesize 1048576 'https://console.example/node-install/node-install.pyz' -o "$d/node-install.pyz" || { c=$?; printf 'Cannot download node installer; check the console URL, TLS and proxy settings.\\n' >&2; exit "$c"; }
printf '==> Verifying node installer...\\n' &&
printf '%s  %s\\n' '${digest}' "$d/node-install.pyz" | sha256sum -c --status &&
$s \${s:+--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY} python3 "$d/node-install.pyz" \${NO_COLOR+--no-color} --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f')`);
    expect(nodeUninstallCommand({ sourceUrl: "https://console.example", installationId: "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", scriptDigest: digest, force: true }).split("\n").at(-1))
      .toBe(`$s \${s:+--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY} python3 "$d/node-install.pyz" \${NO_COLOR+--no-color} --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f' --force)`);
  });
  it("points at the system node journal", () => {
    expect(nodeLogCommand("7f3c2a90-fixture")).toBe("sudo journalctl -u oac-node-7f3c2a90-fixture.service");
    expect(nodeLogCommand("a b")).toBe("sudo journalctl -u 'oac-node-a b.service'");
  });
  // "as root": a root shell runs the sudo command without sudo.
  it.each(["success", "download failure", "checksum mismatch", "installer failure", "as root", "no color", "uppercase proxy", "lowercase proxy", "empty proxy", "root proxy"])("executes safely, passes the token only on stdin and cleans private downloads after %s", (scenario) => {
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
const stdin=fs.readFileSync(0,'utf8');
fs.writeFileSync(process.env.REPORT,JSON.stringify({args:process.argv.slice(2),env:process.env,stdin,mode:fs.statSync(process.argv[2]).mode&511}));
process.exit(process.env.SCENARIO==='installer failure'?7:0);`);
    const digest = scenario === "checksum mismatch" ? "0".repeat(64) : createHash("sha256").update(payload).digest("hex");
    const token = "fixture'one-time";
    try {
      const command = nodeInstallCommand({ token, coreUrl: "http://127.0.0.1:8091", sourceUrl: "http://localhost:8080", provider: "docker", installationId: "fixture-installation", scriptDigest: digest });
      const proxy = "http://fixture:private%20password@proxy.example:3128";
      const proxyEnv: Record<string, string | undefined> = { http_proxy: undefined, https_proxy: undefined, no_proxy: undefined, HTTP_PROXY: undefined, HTTPS_PROXY: undefined, NO_PROXY: undefined, ALL_PROXY: undefined };
      if (["uppercase proxy", "root proxy", "empty proxy"].includes(scenario)) Object.assign(proxyEnv, { HTTP_PROXY: proxy, HTTPS_PROXY: proxy, NO_PROXY: "core.example,localhost" });
      if (scenario === "lowercase proxy") Object.assign(proxyEnv, { http_proxy: proxy, https_proxy: proxy, no_proxy: "core.example,localhost", HTTP_PROXY: "http://wrong.invalid" });
      if (scenario === "empty proxy") proxyEnv.http_proxy = "";
      const result = spawnSync("sh", ["-c", command], { env: { ...process.env, ...proxyEnv, NO_COLOR: scenario === "no color" ? "" : undefined, PATH: `${bin}:${process.env.PATH}`, TMPDIR: temporary, SCENARIO: scenario, FIXTURE: fixture, REPORT: report, SUDO_REPORT: sudoReport, PRINTF_REPORT: printfReport }, encoding: "utf8" });
      expect(result.status).toBe(scenario === "installer failure" ? 7 : scenario === "download failure" ? 22 : scenario === "checksum mismatch" ? 1 : 0);
      expect(result.stdout).toContain("==> Downloading node installer...");
      expect(result.stdout.includes("==> Verifying node installer...")).toBe(scenario !== "download failure");
      expect(result.stdout + result.stderr).not.toContain(token);
      expect(result.stdout + result.stderr).not.toContain("private%20password");
      expect(result.stdout + result.stderr).not.toContain("\u001b[");
      expect(readdirSync(temporary)).toEqual([]);
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
      expect(invocation.stdin).toBe(`${token}\n`);
      expect(invocation.args.some((arg) => arg.includes("one-time"))).toBe(false);
      expect(Object.values(invocation.env).some((value) => value.includes("one-time"))).toBe(false);
      expect(invocation.args.slice(1)).toEqual([...(scenario === "no color" ? ["--no-color"] : []), "--enrollment-token-stdin", "--source-url", "http://localhost:8080", "--core-url", "http://127.0.0.1:8091", "--provider", "docker", "--installation-id", "fixture-installation"]);
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
