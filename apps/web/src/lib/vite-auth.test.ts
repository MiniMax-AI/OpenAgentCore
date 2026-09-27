import { chmodSync, mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { loadProxyBearerAuth, resolveProxyTokenFile } from "../../vite-auth.ts";

const temporaryDirectories: string[] = [];

function makeTemporaryDirectory(): string {
  const parent = join(homedir(), ".oac", "tests");
  mkdirSync(parent, { recursive: true });
  const directory = mkdtempSync(join(parent, "oac-web-auth-"));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

describe("Vite OpenAgentCore proxy authentication", () => {
  it("falls back to browser-managed authentication when the conventional file is absent", () => {
    const homeDir = makeTemporaryDirectory();

    expect(loadProxyBearerAuth({ homeDir })).toBeUndefined();
  });

  it("does not read the retired conventional token path", () => {
    const homeDir = makeTemporaryDirectory();
    const retiredDirectory = join(homeDir, ".parsar", "agents-api");
    mkdirSync(retiredDirectory, { recursive: true });
    writeFileSync(join(retiredDirectory, "web-token"), "retired-token-marker", { mode: 0o600 });
    expect(loadProxyBearerAuth({ homeDir })).toBeUndefined();
  });

  it("expands the conventional home path and reads a private token file", () => {
    const homeDir = makeTemporaryDirectory();
    const stateDirectory = join(homeDir, ".oac", "dev");
    const tokenFile = join(stateDirectory, "web-token");
    mkdirSync(stateDirectory, { recursive: true });
    writeFileSync(tokenFile, "local-tenant-token\n", { mode: 0o600 });
    chmodSync(tokenFile, 0o600);

    expect(resolveProxyTokenFile("~/.oac/dev/web-token", homeDir)).toBe(tokenFile);
    expect(loadProxyBearerAuth({ homeDir })).toEqual({
      token: "local-tenant-token",
      source: "file",
      filePath: tokenFile,
    });
  });

  it("fails closed when an explicitly configured token file cannot be read", () => {
    const rootDir = makeTemporaryDirectory();
    expect(() => loadProxyBearerAuth({ tokenFile: "missing-token", rootDir })).toThrow(
      "Cannot read OpenAgentCore bearer token file",
    );
  });

  it("rejects empty or group/world-accessible token files", () => {
    const rootDir = makeTemporaryDirectory();
    const emptyTokenFile = join(rootDir, "empty-token");
    writeFileSync(emptyTokenFile, "", { mode: 0o600 });
    chmodSync(emptyTokenFile, 0o600);

    expect(() => loadProxyBearerAuth({ tokenFile: emptyTokenFile })).toThrow("is empty");

    if (process.platform !== "win32") {
      const exposedTokenFile = join(rootDir, "exposed-token");
      writeFileSync(exposedTokenFile, "local-tenant-token\n", { mode: 0o644 });
      chmodSync(exposedTokenFile, 0o644);

      expect(() => loadProxyBearerAuth({ tokenFile: exposedTokenFile })).toThrow(
        "must not be group/world accessible",
      );
    }
  });

  it("rejects ambiguous or malformed server-side credentials", () => {
    expect(() => loadProxyBearerAuth({ token: "inline", tokenFile: "/tmp/token" })).toThrow(
      "Set only one",
    );
    expect(() => loadProxyBearerAuth({ token: "two tokens" })).toThrow("without whitespace");
  });
});
