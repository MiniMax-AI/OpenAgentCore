import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type {
  AgentEnvironment,
  AgentEnvironmentResource,
  EnvironmentResourceStatus,
  SessionEnvironmentStatus,
} from "@agents-core-web/agents-client";

import type { LocalDockerGuideProfile } from "../../../lib/docker-guide-config";
import {
  EnvironmentPanel,
  resolveEnvironmentPresentation,
  sanitizeRemoteUrl,
} from "./EnvironmentPanel";
import type { EnvironmentObservation, LiveEnvironmentObservation } from "./environment-state";

const selfHosted: AgentEnvironment = {
  type: "self_hosted",
  id: "environment_01",
  remote_url: "https://user:password@executor.example.test/root?token=private#fragment",
  workspace_directory: "/workspace/<script>alert(1)</script>/project",
  capability_directories: ["/capabilities/one", `/capabilities/${"long/".repeat(80)}`],
};
const canonicalEnvironmentUuid = "0f745b0d-b545-49cd-8d7e-4c31c80dc564";
const hostedEnvironmentUuid = "7a263c51-6bf0-4d53-8518-c792eb1f0d21";
const managedHosted: AgentEnvironment = {
  type: "openai_hosted",
  id: hostedEnvironmentUuid,
  capability_directories: [],
  network: { access: "disabled", allowed_domains: [] },
  packages: { npm: [], python: [], system: [] },
  files: [],
  plugins: [],
  skills: [],
};
const managedResource: AgentEnvironmentResource = {
  id: hostedEnvironmentUuid,
  object: "agent.environment",
  type: "openai_hosted",
  status: "pending",
  files: [],
  plugins: [],
  skills: [],
};

function observation(status: SessionEnvironmentStatus): LiveEnvironmentObservation {
  return {
    source: "live",
    environmentId: "environment_01",
    environmentType: "self_hosted",
    status,
    error: status === "failed" ? {
      code: "environment_failed",
      type: "environment_error",
      message: `Failed at https://user:pass@executor.example/private?token=secret#credential <script>alert(1)</script> Authorization: Bearer auth-secret X-API-Key: header-secret {"api_key":"sk-secret"} executor_key: executor-secret caller_key: caller-secret vault_id: vault-secret ${"x".repeat(400)}`,
    } : null,
    eventId: `event_${status}`,
  };
}

function durableObservation(
  status: EnvironmentResourceStatus,
  environmentId = "environment_01",
): EnvironmentObservation {
  const resource: AgentEnvironmentResource = {
    id: environmentId,
    object: "agent.environment",
    type: "self_hosted",
    status,
    files: [],
    plugins: [],
    skills: [],
  };
  return {
    source: "durable",
    environmentId,
    environmentType: "self_hosted",
    status,
    resource,
  };
}

function render(
  environment: AgentEnvironment,
  live: EnvironmentObservation | null = null,
  dockerGuideProfile: LocalDockerGuideProfile | null = null,
) {
  return renderToStaticMarkup(
    <EnvironmentPanel
      environment={environment}
      observation={live}
      connectionActions={[]}
      dockerGuideProfile={dockerGuideProfile}
    />,
  );
}

describe("EnvironmentPanel", () => {
  it("renders the exact managed profile, Workspace list and durable-gated write without a launcher", () => {
    const html = renderToStaticMarkup(
      <EnvironmentPanel
        environment={managedHosted}
        observation={{
          source: "durable",
          environmentId: hostedEnvironmentUuid,
          environmentType: "openai_hosted",
          status: "pending",
          resource: managedResource,
        }}
        connectionActions={[{ type: "environment_connection", environment_id: hostedEnvironmentUuid }]}
        environmentFilesEnabled
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
        onCreateFile={async (_environmentId, input) => ({
          environment_id: hostedEnvironmentUuid,
          object: "agent.environment.file",
          path: input.path,
          size_bytes: 0,
        })}
      />,
    );

    expect(html).toContain("Managed hosted Environment");
    expect(html).toContain(hostedEnvironmentUuid);
    expect(html).toContain("Network access");
    expect(html).toContain("Disabled");
    expect(html).toContain('value="/workspace"');
    expect(html).toContain("Workspace root <code>/workspace</code>");
    expect(html).toContain("None installed by the basic profile");
    expect(html).toContain("0 capability directories · 0 files · 0 plugins · 0 skills");
    expect(html).toContain("Workspace files");
    expect(html).toContain("Add inline Workspace file");
    expect(html).not.toContain("Connect Environment");
    expect(html).not.toContain("Remote URL");
    expect(html).not.toContain("Linux / VM");
  });

  it("does not expose managed writes until exact same-type durable retrieval qualifies them", () => {
    const renderManaged = (observationValue: EnvironmentObservation | null) => renderToStaticMarkup(
      <EnvironmentPanel
        environment={managedHosted}
        observation={observationValue}
        connectionActions={[]}
        environmentFilesEnabled
        onCreateFile={async () => ({
          environment_id: hostedEnvironmentUuid,
          object: "agent.environment.file",
          path: "/workspace/input.txt",
          size_bytes: 0,
        })}
      />,
    );
    expect(renderManaged(null)).not.toContain("Add inline Workspace file");
    expect(renderManaged({
      source: "live",
      environmentId: hostedEnvironmentUuid,
      environmentType: "openai_hosted",
      status: "connected",
      error: null,
      eventId: "event-hosted",
    })).not.toContain("Add inline Workspace file");
    expect(renderManaged({
      source: "live",
      environmentId: hostedEnvironmentUuid,
      environmentType: "openai_hosted",
      status: "failed",
      error: null,
      eventId: "event-hosted-failed",
      durableResource: managedResource,
    })).not.toContain("Add inline Workspace file");
    expect(renderManaged({
      source: "durable",
      environmentId: hostedEnvironmentUuid,
      environmentType: "self_hosted",
      status: "pending",
      resource: { ...managedResource, type: "self_hosted" },
    })).not.toContain("Add inline Workspace file");
    expect(renderManaged({
      source: "durable",
      environmentId: hostedEnvironmentUuid,
      environmentType: "openai_hosted",
      status: "expired",
      resource: { ...managedResource, status: "expired" },
    })).not.toContain("Add inline Workspace file");
    expect(renderManaged({
      source: "durable",
      environmentId: hostedEnvironmentUuid,
      environmentType: "openai_hosted",
      status: "pending",
      resource: { ...managedResource, files: [{}] } as unknown as AgentEnvironmentResource,
    })).not.toContain("Add inline Workspace file");
  });

  it("does not expose managed Workspace operations for an unsupported populated Session projection", () => {
    const html = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{ ...managedHosted, packages: { npm: ["future-package"], python: [], system: [] } } as unknown as AgentEnvironment}
        observation={{
          source: "durable",
          environmentId: hostedEnvironmentUuid,
          environmentType: "openai_hosted",
          status: "pending",
          resource: managedResource,
        }}
        connectionActions={[]}
        environmentFilesEnabled
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
        onCreateFile={async () => ({
          environment_id: hostedEnvironmentUuid,
          object: "agent.environment.file",
          path: "/workspace/input.txt",
          size_bytes: 0,
        })}
      />,
    );
    expect(html).not.toContain("Workspace files");
    expect(html).not.toContain("Add inline Workspace file");
  });

  it("projects one fail-closed status for both the header trigger and detail panel", () => {
    expect(resolveEnvironmentPresentation({ type: "none" }, null, [])).toMatchObject({
      visible: false,
    });
    expect(resolveEnvironmentPresentation(selfHosted, observation("connected"), [])).toMatchObject({
      visible: true,
      status: "connected",
      statusLabel: "Connected",
      triggerLabel: "Environment connected",
      defaultLauncherGuideOpen: false,
    });
    expect(resolveEnvironmentPresentation(selfHosted, null, [])).toMatchObject({
      status: "unknown",
      triggerLabel: "Environment status unknown",
      defaultLauncherGuideOpen: false,
    });
    expect(resolveEnvironmentPresentation(selfHosted, null, [{
      type: "environment_connection",
      environment_id: "environment_01",
    }])).toMatchObject({
      status: "required",
      triggerLabel: "Connect environment",
      defaultLauncherGuideOpen: true,
    });
    expect(resolveEnvironmentPresentation(
      { type: "future_remote" } as unknown as AgentEnvironment,
      observation("connected"),
      [],
    )).toMatchObject({
      status: "unavailable",
      triggerLabel: "Environment unavailable",
    });
  });

  it("does not render Environment or Workspace UI for environment:none", () => {
    const html = render({ type: "none" });
    expect(html).toBe("");
  });

  it("shows Workspace files only for a complete known Environment and an explicit list capability", () => {
    const complete = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{
          ...selfHosted,
          id: canonicalEnvironmentUuid,
          remote_url: "https://executor.example.test",
          workspace_directory: "/executor/workspace",
          capability_directories: [],
        }}
        observation={null}
        connectionActions={[]}
        environmentFilesEnabled
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
      />,
    );
    expect(complete).toContain("Workspace files");
    expect(complete).toContain("Files are loaded only when requested");
    expect(complete).toContain("/executor/workspace");
    expect(complete).toContain('value="/executor/workspace"');

    const buildDisabled = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{
          ...selfHosted,
          id: canonicalEnvironmentUuid,
          remote_url: "https://executor.example.test",
          workspace_directory: "/executor/workspace",
          capability_directories: [],
        }}
        observation={null}
        connectionActions={[]}
        environmentFilesEnabled={false}
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
      />,
    );
    expect(buildDisabled).not.toContain("Workspace files");

    const withoutCapability = render({
      ...selfHosted,
      id: canonicalEnvironmentUuid,
      remote_url: "https://executor.example.test",
      workspace_directory: "/workspace/project",
      capability_directories: [],
    });
    expect(withoutCapability).not.toContain("Workspace files");

    const incomplete = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{ type: "self_hosted", id: "environment_01" } as AgentEnvironment}
        observation={null}
        connectionActions={[]}
        environmentFilesEnabled
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
      />,
    );
    expect(incomplete).not.toContain("Workspace files");

    const unsupportedProfile = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{
          ...selfHosted,
          id: canonicalEnvironmentUuid,
          remote_url: "https://executor.example.test",
          workspace_directory: "/workspace/project",
          capability_directories: ["/capabilities/unsupported"],
        }}
        observation={null}
        connectionActions={[]}
        environmentFilesEnabled
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
      />,
    );
    expect(unsupportedProfile).not.toContain("Workspace files");
  });

  it("renders a canonical durable UUID for an uppercase Session Environment identity", () => {
    const environment = { ...selfHosted, id: canonicalEnvironmentUuid.toUpperCase() };
    const html = render(environment, durableObservation("connected", canonicalEnvironmentUuid));
    expect(html).toContain("Connected");
    expect(html).toContain("Status comes from the durable Environment resource");
    expect(html).not.toContain("Durable Environment status is unavailable");
  });

  it("sanitizes http(s) remote URLs and rejects other or malformed schemes", () => {
    expect(sanitizeRemoteUrl(selfHosted.remote_url)).toEqual({
      href: "https://executor.example.test/root",
      label: "https://executor.example.test/root",
    });
    expect(sanitizeRemoteUrl("http://127.0.0.1:8091/?key=private#fragment")).toEqual({
      href: "http://127.0.0.1:8091/",
      label: "http://127.0.0.1:8091/",
    });
    expect(sanitizeRemoteUrl("javascript:alert(1)")).toBeNull();
    expect(sanitizeRemoteUrl("file:///workspace/private")).toBeNull();
    expect(sanitizeRemoteUrl("not a url")).toBeNull();

    const html = render(selfHosted, observation("connected"));
    expect(html).toContain("https://executor.example.test/root");
    expect(html).not.toContain('href="https://executor.example.test/root"');
    expect(html).not.toContain("user:password");
    expect(html).not.toContain("token=private");
    expect(html).not.toContain("fragment");
    expect(html).not.toContain("file://");
  });

  it.each(["pending", "ready", "connected", "disconnected", "failed"] as SessionEnvironmentStatus[])(
    "renders the pinned %s live observation without executor inference",
    (status) => {
      const html = render(selfHosted, observation(status));
      expect(html).toContain(status.charAt(0).toUpperCase() + status.slice(1));
      expect(html).toContain("last supported live event observed");
      expect(html).toContain("does not prove executor");
    },
  );

  it.each(["pending", "connected", "disconnected", "expired", "failed"] as EnvironmentResourceStatus[])(
    "renders durable %s after reload without treating inventory as host capability",
    (status) => {
      const html = render(selfHosted, durableObservation(status));
      expect(html).toContain(status.charAt(0).toUpperCase() + status.slice(1));
      expect(html).toContain("Status comes from the durable Environment resource");
      expect(html).toContain("no API-managed files, plugins, or skills");
      expect(html).toContain("not host or Workspace inventory");
      expect(html).toContain("does not prove executor");
      if (status === "expired") {
        expect(html).toContain("Environment expired");
        expect(html).toContain("does not retry or recreate it");
      }
    },
  );

  it("renders a failed durable read as unavailable while leaving Workspace context visible", () => {
    const html = render(selfHosted, {
      source: "unavailable",
      environmentId: "environment_01",
      environmentType: "self_hosted",
      status: null,
    });
    expect(html).toContain("Unavailable");
    expect(html).toContain("conversation remains usable");
    expect(html).toContain("/workspace/&lt;script&gt;alert(1)&lt;/script&gt;/project");
    expect(html).not.toContain("Connected");
    expect(html).not.toContain("Ready");
  });

  it("shows directories only as escaped text and redacts unsafe error content", () => {
    const html = render(selfHosted, observation("failed"));
    expect(html).toContain("/workspace/&lt;script&gt;alert(1)&lt;/script&gt;/project");
    expect(html).toContain("/capabilities/long/");
    expect(html).not.toContain('href="/workspace/');
    expect(html).not.toContain("user:pass");
    expect(html).not.toContain("token=secret");
    expect(html).not.toContain("hidden-value");
    expect(html).not.toContain("auth-secret");
    expect(html).not.toContain("header-secret");
    expect(html).not.toContain("query-secret");
    expect(html).not.toContain("token-secret");
    expect(html).not.toContain("sk-secret");
    expect(html).not.toContain("executor-secret");
    expect(html).not.toContain("caller-secret");
    expect(html).not.toContain("vault-secret");
    expect(html).not.toContain("https://executor.example/private");
    expect(html).toContain("Raw error fields are hidden");
  });

  it("never renders arbitrary Environment error code or type fields", () => {
    const malicious = observation("failed");
    malicious.error = {
      code: "sk-proj-secret-value",
      type: "executor-secret",
      message: "vault_id: private-vault",
    };
    const html = render(selfHosted, malicious);
    expect(html).not.toContain("sk-proj-secret-value");
    expect(html).not.toContain("executor-secret");
    expect(html).not.toContain("private-vault");
    expect(html).toContain("Raw error fields are hidden");
  });

  it("fails closed for unknown types, missing fields, and unsafe URLs", () => {
    const unknown = render({ type: "future_remote", status: "expired", workspace_directory: "/secret" } as unknown as AgentEnvironment);
    expect(unknown).toContain("Environment unavailable");
    expect(unknown).toContain("Unknown type");
    expect(unknown).not.toContain("/secret");

    const missing = render({ type: "self_hosted" } as AgentEnvironment);
    expect(missing).toContain("ID unavailable");
    expect(missing).toContain("unsafe or malformed URL");
    expect(missing).toContain("Workspace directory");
    expect(missing).toContain("Unavailable");

    const unsafe = render({ ...selfHosted, remote_url: "data:text/html,<script>alert(1)</script>" });
    expect(unsafe).toContain("unsafe or malformed URL");
    expect(unsafe).not.toContain("data:text/html");

    const missingEnvironment = render(null as unknown as AgentEnvironment);
    expect(missingEnvironment).toContain("Environment unavailable");
    expect(missingEnvironment).toContain("Unknown type");
  });

  it("uses a durable environment_connection action without claiming connection", () => {
    const html = renderToStaticMarkup(
      <EnvironmentPanel
        environment={selfHosted}
        observation={null}
        connectionActions={[{ type: "environment_connection", environment_id: "environment_01" }]}
      />,
    );
    expect(html).toContain("Connection required");
    expect(html).toContain("No executor availability is inferred");

    const uuidHtml = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{ ...selfHosted, id: canonicalEnvironmentUuid.toUpperCase() }}
        observation={null}
        connectionActions={[{ type: "environment_connection", environment_id: canonicalEnvironmentUuid }]}
      />,
    );
    expect(uuidHtml).toContain("Connection required");
  });

  it("shows a secret-free launcher guide only for a complete safe projection", () => {
    const html = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{
          ...selfHosted,
          id: canonicalEnvironmentUuid,
          remote_url: "https://executor.example.test",
          workspace_directory: "/executor/workspace",
          capability_directories: [],
        }}
        observation={null}
        connectionActions={[{ type: "environment_connection", environment_id: canonicalEnvironmentUuid }]}
      />,
    );
    expect(html).toContain("Connect Environment");
    expect(html).toContain("Copy native command");
    expect(html).toContain("agents-api-codex-executor");
    expect(html).toContain("executor-key.json");
    expect(html).toContain("Web copies its path but never creates, reads, stores, or transmits the key");
    expect(html).not.toContain("executor_token");
    expect(html).not.toContain("Authorization");
    expect(html).toContain(currentCoreDocsForAssertion());

    const expanded = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{
          ...selfHosted,
          id: canonicalEnvironmentUuid,
          remote_url: "https://executor.example.test",
          workspace_directory: "/executor/workspace",
          capability_directories: [],
        }}
        observation={null}
        connectionActions={[{ type: "environment_connection", environment_id: canonicalEnvironmentUuid }]}
        defaultLauncherGuideOpen
      />,
    );
    expect(expanded).toContain('<details class="environment-launcher-guide" open="">');

    const unsafe = renderToStaticMarkup(
      <EnvironmentPanel
        environment={{ ...selfHosted, id: canonicalEnvironmentUuid }}
        observation={null}
        connectionActions={[{ type: "environment_connection", environment_id: canonicalEnvironmentUuid }]}
      />,
    );
    expect(unsafe).toContain("Connect Environment unavailable");
    expect(unsafe).not.toContain("Copy native command");
  });

  it("shows an operator-configured Docker recipe without hiding the native fallback", () => {
    const dockerProfile: LocalDockerGuideProfile = {
      image: "agents-core-web-executor:2b34ea46-codex-0.153.4",
      apiContainer: "agents-core-web-api",
      user: "501:20",
      credentialsHomePath: ".parsar/agents-api-web-smoke/executor-key.json",
      runtimeHomePath: ".parsar/agents-api-web-smoke/executors",
    };
    const html = render({
      ...selfHosted,
      id: canonicalEnvironmentUuid,
      remote_url: "http://127.0.0.1:8091",
      workspace_directory: "/good",
      capability_directories: [],
    }, null, dockerProfile);
    expect(html).toContain("Docker");
    expect(html).toContain("Linux / VM");
    expect(html).toContain("Copy Docker command");
    expect(html).toContain("docker run --detach");
    expect(html).toContain("HOST_WORKSPACE_DIRECTORY");
    expect(html).toContain("/good");
    expect(html).not.toContain("executor_token");
  });
});

function currentCoreDocsForAssertion(): string {
  return "/parsar-core/blob/main/";
}
