import { Info, RefreshCw } from "lucide-react";

import type { CoreStartupConfiguration } from "@agents-core-web/agents-client";

import { StatusIcon, type StatusKind } from "../../components/StatusIcon";
import type { CoreConnectionState } from "../../lib/connection";

import "./SystemView.css";

function stateKind(state: CoreConnectionState): StatusKind {
  if (state === "ready") return "completed";
  if (state === "failed") return "failed";
  return "running";
}

function stateLabel(state: CoreConnectionState): string {
  if (state === "ready") return "Available";
  if (state === "failed") return "Unavailable";
  return "Checking…";
}

function harnessLabel(harness: string): string {
  if (harness === "claude_sdk") return "Claude SDK";
  if (harness === "mcode") return "MiniMax Code";
  if (harness === "codex") return "Codex";
  return harness;
}

function providerLabel(provider: string | null): string {
  if (provider === "microsandbox") return "Microsandbox";
  if (provider === "docker") return "Docker";
  return "None";
}

export function safeCoreBaseUrlLabel(value: string): string {
  const candidate = value.trim() || "/v1";
  if (candidate.startsWith("/")) return candidate;
  try {
    const url = new URL(candidate);
    url.username = "";
    url.password = "";
    url.search = "";
    url.hash = "";
    return url.toString().replace(/\/$/, "");
  } catch {
    return "Configured Core";
  }
}

interface SystemStatusCard {
  detail: string;
  label: string;
  status: StatusKind;
  value: string;
}

function StartupStatus({ enabled, label }: { enabled: boolean; label?: string }) {
  return (
    <span className={`system-config-status ${enabled ? "enabled" : "disabled"}`}>
      <span aria-hidden="true" />
      {label ?? (enabled ? "Enabled" : "Not configured")}
    </span>
  );
}

export function SystemView({
  coreState,
  coreBaseUrl,
  startupConfiguration,
  startupConfigurationState,
  startupConfigurationSupported,
  vaultCollectionState,
  vaultSupported,
  selfHostedWebEnabled,
  managedWebEnabled,
  refreshing,
  onRefresh,
}: {
  coreState: CoreConnectionState;
  coreBaseUrl: string;
  startupConfiguration: CoreStartupConfiguration | null;
  startupConfigurationState: CoreConnectionState;
  startupConfigurationSupported: boolean | null;
  vaultCollectionState: CoreConnectionState;
  vaultSupported: boolean | null;
  selfHostedWebEnabled: boolean;
  managedWebEnabled: boolean;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const configuration = startupConfigurationState === "ready" && startupConfigurationSupported === true
    ? startupConfiguration
    : null;
  const configuredEndpoints = configuration?.configured.model_providers.filter((provider) => provider.endpoint_configured).length ?? 0;
  const endpointTotal = configuration?.configured.model_providers.length ?? 0;
  const startupUnavailable = startupConfigurationSupported === false;

  const startupValue = (value: string): string => {
    if (configuration) return value;
    if (startupUnavailable) return "Not exposed";
    if (startupConfigurationState === "failed") return "Unavailable";
    return "Checking…";
  };
  const startupStatus: StatusKind = configuration
    ? "completed"
    : startupUnavailable
      ? "interrupted"
      : stateKind(startupConfigurationState);
  const startupDetail = startupUnavailable
    ? "This Core version does not expose the startup configuration extension."
    : startupConfigurationState === "failed"
      ? "The startup configuration request failed. No configuration is inferred."
      : "Reported by the safe Core startup configuration extension.";
  const vaultStatus: SystemStatusCard = vaultSupported === false
    ? { label: "Vaults", status: "interrupted", value: "Unavailable", detail: "This Core does not expose the Vaults API." }
    : vaultCollectionState === "failed"
      ? { label: "Vaults", status: "failed", value: vaultSupported === true ? "Refresh failed" : "Check failed", detail: "The Vault catalog request failed." }
      : vaultSupported === true && vaultCollectionState === "ready"
        ? { label: "Vaults", status: "completed", value: "Available", detail: "Vault catalog loaded." }
        : { label: "Vaults", status: "running", value: "Checking…", detail: "Checking whether this Core exposes the Vaults API." };

  const cards: SystemStatusCard[] = [
    {
      label: "Core API",
      status: stateKind(coreState),
      value: stateLabel(coreState),
      detail: `${safeCoreBaseUrlLabel(coreBaseUrl)} · ${
        coreState === "ready"
          ? "confirmed by an Agent or Session API request"
          : coreState === "failed"
            ? "Agent and Session API requests failed"
            : "checking Agent and Session APIs"
      }.`,
    },
    vaultStatus,
    {
      label: "Default harness",
      status: startupStatus,
      value: startupValue(configuration ? harnessLabel(configuration.configured.default_harness) : ""),
      detail: startupDetail,
    },
    {
      label: "Managed sandbox",
      status: startupStatus,
      value: startupValue(configuration ? providerLabel(configuration.configured.managed_sandbox.provider) : ""),
      detail: configuration?.configured.managed_sandbox.maintenance
        ? "Selected provider is in maintenance mode."
        : startupDetail,
    },
    {
      label: "LLM endpoints",
      status: startupStatus,
      value: startupValue(endpointTotal === 0 || configuredEndpoints === 0
        ? "Not configured"
        : configuredEndpoints === endpointTotal ? "Configured" : `${configuredEndpoints}/${endpointTotal} configured`),
      detail: configuration
        ? "Reports operator endpoint configuration presence only; addresses and credentials stay hidden."
        : startupDetail,
    },
  ];

  return (
    <section className="page-section architecture-page system-page" aria-labelledby="system-heading">
      <header className="page-header">
        <h1 id="system-heading">System <span>Core startup configuration</span></h1>
        <div className="page-actions">
          <button
            className="button outline"
            type="button"
            onClick={onRefresh}
            disabled={refreshing}
            aria-label={refreshing ? "Refreshing System status" : "Refresh System status"}
          >
            <RefreshCw className={refreshing ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} aria-hidden="true" />
            {refreshing ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </header>

      <div className="system-summary" role="list" aria-label="System startup summary" aria-live="polite" aria-busy={refreshing}>
        {cards.map((card) => (
          <div className="system-summary-cell" role="listitem" key={card.label}>
            <span><StatusIcon status={card.status} />{card.label}</span>
            <strong>{card.value}</strong>
            <small>{card.detail}</small>
          </div>
        ))}
      </div>

      {configuration ? (
        <>
          <section className="system-config-section" aria-labelledby="configured-startup-heading">
            <header>
              <h2 id="configured-startup-heading">Configured for this process</h2>
              <span>Validated or detected when Core started</span>
            </header>
            <div className="system-config-list">
              <div className="system-config-row">
                <strong>Daemon gateway</strong>
                <StartupStatus enabled={configuration.configured.daemon_gateway} />
                <small>Accepts authenticated execution Runtime connections when enabled.</small>
              </div>
              <div className="system-config-row">
                <strong>Managed execution</strong>
                <StartupStatus
                  enabled={configuration.configured.managed_sandbox.enabled}
                  label={configuration.configured.managed_sandbox.enabled
                    ? `${providerLabel(configuration.configured.managed_sandbox.provider)}${configuration.configured.managed_sandbox.maintenance ? " · Maintenance" : ""}`
                    : undefined}
                />
                <small>
                  Supported by this build: {configuration.supported.managed_sandbox_providers.map(providerLabel).join(", ")}.
                  {managedWebEnabled ? " This Web build can request managed Sessions." : " This Web build cannot request managed Sessions."}
                </small>
              </div>
              <div className="system-config-row">
                <strong>Self-hosted execution</strong>
                <StartupStatus enabled={configuration.configured.self_hosted} />
                <small>{selfHostedWebEnabled ? "This Web build can request self-hosted Sessions." : "This Web build cannot request self-hosted Sessions."}</small>
              </div>
            </div>
          </section>

          <section className="system-config-section" aria-labelledby="configured-harnesses-heading">
            <header>
              <h2 id="configured-harnesses-heading">Harnesses</h2>
              <span>Configuration, not execution readiness</span>
            </header>
            <div className="system-harness-grid">
              {configuration.supported.harnesses.map((harness) => {
                const enabled = configuration.configured.enabled_harnesses.includes(harness);
                const endpoint = configuration.configured.model_providers.find((provider) => provider.harness === harness);
                return (
                  <article className="system-harness-card" key={harness}>
                    <div>
                      <strong>{harnessLabel(harness)}</strong>
                      <StartupStatus enabled={enabled} label={enabled ? "Configured" : "Supported"} />
                    </div>
                    <small>
                      {enabled
                        ? `Operator LLM endpoint: ${endpoint?.endpoint_configured ? "configured" : "not configured"}.`
                        : "Known by this build but not enabled for this process."}
                    </small>
                  </article>
                );
              })}
            </div>
          </section>

          <p className="system-boundary-note"><Info size={15} aria-hidden="true" />These values describe Core startup configuration only. Runtime connection, native binary availability, sandbox health, and model execution belong to the relevant Session or Environment.</p>
        </>
      ) : null}
    </section>
  );
}
