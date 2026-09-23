import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import {
  ConnectionModal,
  ConnectionProbeStatus,
  type ConnectionProbeState,
} from "./ConnectionModal";
import type { LocalDockerBackendGuideProfile } from "../lib/docker-guide-config";

const callbacks = {
  onClose: () => undefined,
  onSave: () => undefined,
};

function renderModal(
  baseUrl: string,
  proxyAuthEnabled: boolean,
  token = "",
  dockerBackendGuide: LocalDockerBackendGuideProfile | null = null,
) {
  return renderToStaticMarkup(
    <ConnectionModal
      connection={{ baseUrl, token }}
      open
      proxyAuthEnabled={proxyAuthEnabled}
      dockerBackendGuide={dockerBackendGuide}
      {...callbacks}
    />,
  );
}

describe("Agent Core connection modes", () => {
  it("makes the fixed local Parsar path a zero-input default", () => {
    const markup = renderModal("/v1", true);

    expect(markup).toContain("Local Parsar Core");
    expect(markup).toContain("Default · zero input");
    expect(markup).toMatch(/type="radio"[^>]*checked[^>]*value="local"/);
    expect(markup).toContain("Local `/v1` proxy");
    expect(markup).toContain("No URL is required");
    expect(markup).not.toContain("Compatible Core base URL");
    expect(markup).not.toContain('type="password"');
  });

  it("does not request or expose a browser bearer when proxy auth is active", () => {
    const markup = renderModal("/v1", true, "stale-browser-token");

    expect(markup).toContain("Server-managed key detected");
    expect(markup).toContain("No bearer credential is exposed to browser JavaScript");
    expect(markup).not.toContain("stale-browser-token");
    expect(markup).not.toContain("Bearer token");
    expect(markup).not.toContain("sessionStorage");
  });

  it("keeps local mode token-free when server-managed auth is not detected", () => {
    const markup = renderModal("/v1", false, "stale-browser-token");

    expect(markup).toContain("Server-managed key not detected");
    expect(markup).toContain("Configure the local proxy token file and restart Web");
    expect(markup).toContain("Local mode will not request a browser token");
    expect(markup).not.toContain("stale-browser-token");
    expect(markup).not.toContain('type="password"');
  });

  it("puts direct URLs, CORS guidance, and current-tab tokens behind Advanced", () => {
    const markup = renderModal("http://127.0.0.1:8091/v1", true);

    expect(markup).toMatch(/type="radio"[^>]*checked[^>]*value="advanced"/);
    expect(markup).toContain("Compatible Core base URL");
    expect(markup).toContain("through CORS");
    expect(markup).toContain('type="password"');
    expect(markup).toContain("Current tab only");
    expect(markup).toContain("sessionStorage, never localStorage");
    expect(markup).not.toContain("Server-managed key detected");
  });

  it("rejects credentials and query data embedded in an Advanced URL", () => {
    const markup = renderModal("https://core.example/v1?token=unsafe", true);

    expect(markup).toContain("an HTTP loopback URL, without credentials, query parameters, or fragments");
    expect(markup).toContain('aria-invalid="true"');
    expect(markup.match(/disabled=""/g)).toHaveLength(2);
  });

  it("rejects remote HTTP while explaining the HTTPS and loopback boundary", () => {
    const markup = renderModal("http://core.example/v1", true, "must-not-leave-browser");

    expect(markup).toContain("Remote Core access requires HTTPS");
    expect(markup).toContain("Plain HTTP is allowed only for an explicit loopback host");
    expect(markup).toContain('aria-invalid="true"');
    expect(markup.match(/disabled=""/g)).toHaveLength(2);
  });

  it("keeps credential tutorials in current monorepo operator links", () => {
    const markup = renderModal("/v1", true);

    expect(markup).toContain("Operator-owned setup");
    expect(markup).toContain("Web connection guide · current repository");
    expect(markup).toContain("Connection troubleshooting · current repository");
    expect(markup).toContain("Parsar Core setup");
    expect(markup).toContain("parsar-core/blob/main/docs/web/core-connection.md");
    expect(markup).toContain("parsar-core/blob/main/services/agents-api/README.md");
    expect(markup).not.toContain("MiniMax-AI-Dev/parsar/blob");
    expect(markup).not.toContain("0438880ab21aa16d05cb91a4c7f91cc0abc12358");
    expect(markup).not.toContain("f7cdf591396529880d80f8211fc7a0f4768fdf46");
    expect(markup).not.toContain("8cc2898ca42b272cb3771234ee6a0ad0d2e932ba");
    expect(markup).not.toContain("7409e00ca25311805a9f8f0d03614f820e407642");
    expect(markup).not.toContain("6345391");
    expect(markup).not.toContain("bac551c");
    expect(markup).not.toContain("openssl rand");
    expect(markup).not.toContain("AGENTS_API_KEYS_FILE");
    expect(markup).not.toContain("AGENTS_API_DAEMON_WS_URL");
  });

  it("renders a copyable, non-executing Docker backend recovery guide", () => {
    const markup = renderModal("/v1", true, "", {
      databaseContainer: "parsar-agents-api-web-smoke-db",
      apiContainer: "agents-core-web-api",
      daemonContainer: "agents-core-web-daemon",
      corePort: 8091,
    });

    expect(markup).toContain("Start a local Docker backend");
    expect(markup).toContain("docker start parsar-agents-api-web-smoke-db");
    expect(markup).toContain("docker start agents-core-web-api agents-core-web-daemon");
    expect(markup).toContain("http://127.0.0.1:8091/healthz");
    expect(markup).toContain("Already set up on this computer");
    expect(markup).toContain("First time on this computer");
    expect(markup).toContain("make docker-build-agents-api");
    expect(markup).toContain("does not publish a safe zero-input bootstrap");
    expect(markup).toContain("Core container setup");
    expect(markup).toContain("Daemon provisioning");
    expect(markup).toContain(`/parsar-core/blob/main/services/agents-api/CONTAINER.md`);
    expect(markup).toContain("Web only displays the reviewed commands");
    expect(markup).toContain("never receives Docker socket access or executes them");
    expect(markup).toContain("A self-hosted executor is Session-specific");
    expect(markup).not.toContain("docker run");
    expect(markup).not.toContain("docker compose down");
  });

  it("fails closed to configuration help when container names are unavailable", () => {
    const markup = renderModal("/v1", true);

    expect(markup).toContain("Docker startup guide is not configured for this Web build");
    expect(markup).toContain("AGENTS_CORE_WEB_DOCKER_BACKEND_*");
    expect(markup).not.toContain("docker start");
  });

  it("states the GET-only probe boundary without disabling the current chat contract", () => {
    const markup = renderModal("/v1", true);

    expect(markup).toContain("Test connection");
    expect(markup).toContain("with one GET");
    expect(markup).toContain("never creates an Agent, Session, Turn, or Item");
    expect(markup).toContain("Chat uses the current Core events contract");
    expect(markup).toContain("a real request can still fail");
  });
});

describe("Connection probe status", () => {
  it("announces the loading state", () => {
    const markup = renderToStaticMarkup(<ConnectionProbeStatus state={{ status: "loading" }} />);

    expect(markup).toContain('role="status"');
    expect(markup).toContain('aria-live="polite"');
    expect(markup).toContain("Testing Core connection…");
    expect(markup).toContain("one read-only Agents API GET request");
  });

  it.each<[ConnectionProbeState, string]>([
    [
      {
        status: "complete",
        result: { kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 },
      },
      "Core API authenticated",
    ],
    [
      {
        status: "complete",
        result: { kind: "invalid_configuration", executionReadiness: "unknown" },
      },
      "Core URL blocked",
    ],
    [
      {
        status: "complete",
        result: { kind: "unauthorized", executionReadiness: "unknown", httpStatus: 401 },
      },
      "rejected the caller key (HTTP 401)",
    ],
    [
      {
        status: "complete",
        result: { kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: 400 },
      },
      "Agents API protocol mismatch",
    ],
    [
      {
        status: "complete",
        result: { kind: "unreachable", executionReadiness: "unknown" },
      },
      "Core unreachable",
    ],
    [
      {
        status: "complete",
        result: { kind: "http_error", executionReadiness: "unknown", httpStatus: 503 },
      },
      "Core returned HTTP 503",
    ],
  ])("renders a safe, distinct terminal result", (state, expected) => {
    const markup = renderToStaticMarkup(<ConnectionProbeStatus state={state} />);

    expect(markup).toContain(expected);
    expect(markup).toContain("Chat uses the current Agents API contract");
    expect(markup).toContain("does not start a Turn or verify its runtime dependencies");
  });

  it("uses an alert for failures and a polite status for authenticated access", () => {
    const failed = renderToStaticMarkup(
      <ConnectionProbeStatus
        state={{
          status: "complete",
          result: { kind: "unreachable", executionReadiness: "unknown" },
        }}
      />,
    );
    const succeeded = renderToStaticMarkup(
      <ConnectionProbeStatus
        state={{
          status: "complete",
          result: { kind: "authenticated", executionReadiness: "unknown", httpStatus: 200 },
        }}
      />,
    );

    expect(failed).toContain('role="alert"');
    expect(succeeded).toContain('role="status"');
  });
});
