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
  startupConfiguration,
  startupConfigurationState,
  startupConfigurationSupported,
  selfHostedWebEnabled,
  managedWebEnabled,
  refreshing,
  onRefresh,
}: {
  startupConfiguration: CoreStartupConfiguration | null;
  startupConfigurationState: CoreConnectionState;
  startupConfigurationSupported: boolean | null;
  selfHostedWebEnabled: boolean;
  managedWebEnabled: boolean;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const configuration = startupConfigurationState === "ready" && startupConfigurationSupported === true
    ? startupConfiguration
    : null;
  const endpointOverrideConfigured = configuration?.configured.model_providers.some((provider) => provider.endpoint_configured) ?? false;
  const executionAdaptersEnabled = (configuration?.configured.enabled_harnesses.length ?? 0) > 0;
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
  const cards: SystemStatusCard[] = [
    {
      label: "Daemon gateway",
      status: startupStatus,
      value: startupValue(configuration?.configured.daemon_gateway ? "Enabled" : "Not configured"),
      detail: configuration
        ? configuration.configured.daemon_gateway
          ? `Accepts authenticated execution Runtime connections. ${selfHostedWebEnabled ? "This Web build can request self-hosted Sessions." : "This Web build cannot request self-hosted Sessions."}`
          : "No execution Runtime gateway is enabled for this Core process."
        : startupDetail,
    },
    {
      label: "Managed sandbox",
      status: startupStatus,
      value: startupValue(configuration
        ? `${providerLabel(configuration.configured.managed_sandbox.provider)}${configuration.configured.managed_sandbox.maintenance ? " · Maintenance" : ""}`
        : ""),
      detail: configuration
        ? `${configuration.configured.managed_sandbox.maintenance ? "Selected provider is in maintenance mode. " : ""}Supported by this build: ${configuration.supported.managed_sandbox_providers.map(providerLabel).join(", ")}. ${managedWebEnabled ? "This Web build can request managed Sessions." : "This Web build cannot request managed Sessions."}`
        : startupDetail,
    },
    {
      label: "Endpoint overrides",
      status: startupStatus,
      value: startupValue(endpointOverrideConfigured ? "Configured" : "Not configured"),
      detail: configuration
        ? endpointOverrideConfigured
          ? "Explicit operator overrides are shown per adapter below; others may use native defaults."
          : executionAdaptersEnabled
            ? "No explicit operator overrides; enabled adapters may use native defaults."
            : "No execution adapters are enabled for this Core process."
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
          <section className="system-config-section" aria-labelledby="configured-harnesses-heading">
            <header>
              <h2 id="configured-harnesses-heading">Execution adapters <span>(harnesses)</span></h2>
              <span>Build support vs startup enablement</span>
            </header>
            <p className="system-config-explanation">
              {executionAdaptersEnabled
                ? <>Agent create and edit forms can select any adapter enabled for this Core process.</>
                : <>This Core process has no daemon gateway, so no execution adapters are active.</>}
            </p>
            <div className="system-harness-grid">
              {configuration.supported.harnesses.map((harness) => {
                const enabled = configuration.configured.enabled_harnesses.includes(harness);
                const endpoint = configuration.configured.model_providers.find((provider) => provider.harness === harness);
                return (
                  <article className="system-harness-card" key={harness}>
                    <div>
                      <strong>{harnessLabel(harness)}</strong>
                      <StartupStatus
                        enabled={enabled}
                        label={enabled ? "Enabled" : "Build only"}
                      />
                    </div>
                    <small>
                      {enabled
                        ? endpoint?.endpoint_configured
                          ? "Operator endpoint override: configured."
                          : "Operator endpoint override: not set; the harness may use its native default."
                        : "Compiled into this build, but not enabled when this Core process started."}
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
