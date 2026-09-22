import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { CoreStartupConfiguration } from "@agents-core-web/agents-client";

import { safeCoreBaseUrlLabel, SystemView } from "./SystemView";

const startup: CoreStartupConfiguration = {
  object: "agents.core.startup_configuration",
  schema_version: 1,
  supported: {
    harnesses: ["claude_sdk", "codex", "mcode"],
    managed_sandbox_providers: ["docker", "microsandbox"],
  },
  configured: {
    default_harness: "codex",
    enabled_harnesses: ["claude_sdk", "codex"],
    daemon_gateway: true,
    self_hosted: true,
    managed_sandbox: { enabled: true, provider: "docker", maintenance: false },
    model_providers: [
      { harness: "claude_sdk", endpoint_configured: false },
      { harness: "codex", endpoint_configured: true },
    ],
  },
};

function render(overrides: Partial<Parameters<typeof SystemView>[0]> = {}): string {
  return renderToStaticMarkup(
    <SystemView
      coreState="ready"
      coreBaseUrl="https://user:pass@core.example/v1?token=secret#fragment"
      startupConfiguration={startup}
      startupConfigurationState="ready"
      startupConfigurationSupported
      vaultCollectionState="ready"
      vaultSupported
      selfHostedWebEnabled
      managedWebEnabled
      refreshing={false}
      onRefresh={() => undefined}
      {...overrides}
    />,
  );
}

describe("SystemView", () => {
  it("renders startup support and configuration without claiming Runtime readiness", () => {
    const html = render();

    expect(html).toContain("Core startup configuration");
    expect(html.match(/role="listitem"/g)).toHaveLength(5);
    expect(html).toContain("Vault catalog loaded");
    expect(html).toContain("Default adapter");
    expect(html).toContain("Managed sandbox");
    expect(html).toContain("Endpoint overrides");
    expect(html).toContain("<strong>Configured</strong>");
    expect(html).not.toContain("1 explicit");
    expect(html).toContain("Explicit operator overrides are shown per adapter below; others may use native defaults");
    expect(html).toContain("Configured for this process");
    expect(html).toContain("Daemon gateway");
    expect(html).toContain("Supported by this build: Docker, Microsandbox");
    expect(html).toContain("Claude SDK");
    expect(html).toContain("MiniMax Code");
    expect(html).toContain("Execution adapters");
    expect(html).toContain("Used by Agents created in this Web UI");
    expect(html).toContain("This UI does not expose adapter selection. API requests can select any enabled adapter");
    expect(html).not.toContain("Enabled · default");
    expect(html).toContain("Operator endpoint override: configured");
    expect(html).toContain("Operator endpoint override: not set; the harness may use its native default");
    expect(html).toContain("Build only");
    expect(html).toContain("Runtime connection, native binary availability, sandbox health, and model execution belong to the relevant Session or Environment");
    expect(html).not.toContain("Runtime status");
    expect(html).not.toContain("ready");
    expect(html).not.toContain("user:pass");
    expect(html).not.toContain("token=secret");
    expect(html).not.toContain("fragment");
  });

  it("shows maintenance and Web-build boundaries separately from Core configuration", () => {
    const maintenance: CoreStartupConfiguration = {
      ...startup,
      configured: {
        ...startup.configured,
        managed_sandbox: { enabled: true, provider: "microsandbox", maintenance: true },
      },
    };
    const html = render({ startupConfiguration: maintenance, selfHostedWebEnabled: false, managedWebEnabled: false });

    expect(html).toContain("Microsandbox · Maintenance");
    expect(html).toContain("Selected provider is in maintenance mode");
    expect(html).toContain("This Web build cannot request managed Sessions");
    expect(html).toContain("This Web build cannot request self-hosted Sessions");
  });

  it("distinguishes build support from an inactive execution surface", () => {
    const inactive: CoreStartupConfiguration = {
      ...startup,
      configured: {
        ...startup.configured,
        enabled_harnesses: [],
        daemon_gateway: false,
        self_hosted: false,
        managed_sandbox: { enabled: false, provider: null, maintenance: false },
        model_providers: [],
      },
    };
    const html = render({ startupConfiguration: inactive });

    expect(html).toContain("<strong>Not configured</strong>");
    expect(html).not.toContain("0 explicit");
    expect(html).toContain("No execution adapters are enabled for this Core process");
    expect(html).toContain("Configured default; not active for execution in this Core process");
    expect(html).toContain("This Core process has no daemon gateway, so no execution adapters are active");
    expect(html.match(/Build only/g)).toHaveLength(3);
    expect(html).not.toContain("Enabled · default");
  });

  it("does not treat native endpoint defaults as unavailable", () => {
    const nativeDefaults: CoreStartupConfiguration = {
      ...startup,
      configured: {
        ...startup.configured,
        model_providers: startup.configured.model_providers.map((provider) => ({
          ...provider,
          endpoint_configured: false,
        })),
      },
    };
    const html = render({ startupConfiguration: nativeDefaults });

    expect(html).toContain("<strong>Not configured</strong>");
    expect(html).toContain("No explicit operator overrides; enabled adapters may use native defaults");
    expect(html).not.toContain("Enabled · default");
  });

  it("handles an older Core without leaving a pending state", () => {
    const html = render({
      startupConfiguration: null,
      startupConfigurationState: "ready",
      startupConfigurationSupported: false,
    });

    expect(html).toContain("Not exposed");
    expect(html).toContain("This Core version does not expose the startup configuration extension");
    expect(html).not.toContain("Configured for this process");
    expect(html).not.toContain("Checking…");
  });

  it("fails closed when the startup configuration read fails", () => {
    const html = render({
      startupConfiguration: null,
      startupConfigurationState: "failed",
      startupConfigurationSupported: null,
    });

    expect(html).toContain("Unavailable");
    expect(html).toContain("No configuration is inferred");
    expect(html).not.toContain("Configured for this process");
  });

  it("sanitizes Core labels independently from connection storage", () => {
    expect(safeCoreBaseUrlLabel("/v1")).toBe("/v1");
    expect(safeCoreBaseUrlLabel("https://user:pass@core.example/v1?secret=1#token")).toBe("https://core.example/v1");
    expect(safeCoreBaseUrlLabel("not a URL")).toBe("Configured Core");
  });
});
