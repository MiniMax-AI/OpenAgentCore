import { describe, expect, it } from "vitest";

import { assertCurrentWebSettings } from "../../vite-settings.ts";

const replacements = [
  ["AGENTS_API_PROXY_TARGET", "OAC_WEB_DEV_PROXY_TARGET"],
  ["AGENTS_API_PROXY_TOKEN", "OAC_WEB_DEV_PROXY_TOKEN"],
  ["AGENTS_API_PROXY_TOKEN_FILE", "OAC_WEB_DEV_PROXY_TOKEN_FILE"],
  ...[
    "SELF_HOSTED_SESSIONS", "OPENAI_HOSTED_SESSIONS", "ENVIRONMENT_FILES",
    "DOCKER_GUIDE", "DOCKER_IMAGE", "DOCKER_API_CONTAINER", "DOCKER_USER",
    "DOCKER_CREDENTIALS_HOME_PATH", "DOCKER_RUNTIME_HOME_PATH", "DOCKER_BACKEND_GUIDE",
    "DOCKER_DATABASE_CONTAINER", "DOCKER_DAEMON_CONTAINER", "DOCKER_CORE_PORT",
  ].map((suffix) => [`AGENTS_CORE_WEB_${suffix}`, `OAC_WEB_${suffix}`]),
];

describe("retired Web settings", () => {
  it.each(replacements)("rejects %s with its replacement, even if the replacement is set", (oldName, newName) => {
    for (const value of ["", "private-value-marker"]) {
      expect(() => assertCurrentWebSettings({ [oldName]: value, [newName]: "1" })).toThrow(
        new Error(`Retired Web settings: ${oldName} is no longer supported; use ${newName}.`),
      );
    }
  });

  it("reports every retired name without echoing values", () => {
    expect(() => assertCurrentWebSettings({
      AGENTS_API_PROXY_TARGET: "https://private-host-marker.example",
      AGENTS_API_PROXY_TOKEN: "private-token-marker",
    })).toThrow(new Error("Retired Web settings: AGENTS_API_PROXY_TARGET is no longer supported; use OAC_WEB_DEV_PROXY_TARGET. AGENTS_API_PROXY_TOKEN is no longer supported; use OAC_WEB_DEV_PROXY_TOKEN."));
  });

  it("accepts current settings and does not retire other components' names by prefix", () => {
    expect(() => assertCurrentWebSettings({
      ...Object.fromEntries(replacements.map(([, name]) => [name, "1"])),
      AGENTS_API_ENGINE: "codex", AGENTS_API_PROXY_UNRELATED: "1",
    })).not.toThrow();
  });
});
