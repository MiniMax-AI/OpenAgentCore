import { describe, expect, it } from "vitest";

import type { SandboxConsoleConfig } from "../sandbox/console-config";
import { executorInstall } from "./executor-install";

const config = (installer: boolean, digest = "c".repeat(64)): SandboxConsoleConfig => ({
  sandbox_admin: true, node_installer: false, node_installer_sha256: "", self_hosted_installer: installer, self_hosted_installer_sha256: digest,
});
const input = { publicUrl: "https://core.example", localOnly: false, environmentId: "5b1e2c3d-0000-4000-8000-000000000001", remoteUrl: "wss://core.example/api/v1/agent-daemon/ws" };

describe("the Connect a host panel", () => {
  it("shows the command when the installer is offered, Core has a public address and the remote is wss", () => {
    const install = executorInstall({ config: config(true), ...input });
    expect(install).toMatchObject({ kind: "ready", publicUrl: "https://core.example" });
    expect(install.kind === "ready" && install.command).toContain(`--environment-id '${input.environmentId}' --remote '${input.remoteUrl}'`);
  });
  it.each([
    ["no console configuration", null],
    ["the installer off", config(false)],
    ["a malformed digest", config(true, "C".repeat(64))],
    ["a short digest", config(true, "c".repeat(63))],
  ])("hides the panel with %s", (_, value) => {
    expect(executorInstall({ config: value, ...input })).toEqual({ kind: "unavailable" });
  });
  it("asks for a public address instead of a command when Core has none", () => {
    expect(executorInstall({ config: config(true), ...input, publicUrl: null })).toEqual({ kind: "no_address" });
  });
  it.each(["https://localhost", "https://[::1]:8443"])("explains instead of showing a command when the public address %s is loopback", (publicUrl) => {
    expect(executorInstall({ config: config(true), ...input, publicUrl, localOnly: true, remoteUrl: publicUrl.replace("https", "wss") + "/api/v1/agent-daemon/ws" })).toEqual({ kind: "local_only", publicUrl });
  });
  it.each(["ws://127.0.0.1:8091/api/v1/agent-daemon/ws", "https://core.example/api/v1/agent-daemon/ws", ""])("explains instead of showing a command for the remote %j", (remoteUrl) => {
    expect(executorInstall({ config: config(true), ...input, remoteUrl })).toEqual({ kind: "not_wss", remoteUrl });
  });
});
