import { afterEach, describe, expect, it, vi } from "vitest";
import { defaultSandboxResources, distributionRuntime, savedSpecification, validSandboxResources } from "./deployment-specification";
import { isRuntimeReleaseField } from "./runtime-release";
import standardSizes from "./standard-sizes.json";
import type { SandboxSpecification } from "@agents-core-web/agents-client";

const manifest = { platform: "linux/amd64", source_commit: "0".repeat(40), images: { runtime: `sha256:${"a".repeat(64)}` },
  image_manifest_digests: { runtime: `sha256:${"b".repeat(64)}` }, runtime_ref: `parsar-core-runtime@sha256:${"b".repeat(64)}`,
  microsandbox: { runtime_sha256: "c".repeat(64), firmware_sha256: "d".repeat(64) } };
afterEach(() => vi.unstubAllGlobals());

describe("deployment resources and Runtime", () => {
  it("maps the exact six identities from one matched distribution", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(manifest)));
    vi.stubGlobal("fetch", fetcher);
    expect(await distributionRuntime(new AbortController().signal)).toEqual({ source_commit: manifest.source_commit,
      image_id: manifest.images.runtime, image_manifest_digest: manifest.image_manifest_digests.runtime,
      microsandbox_ref: manifest.runtime_ref, runtime_sha256: manifest.microsandbox.runtime_sha256, firmware_sha256: manifest.microsandbox.firmware_sha256 });
    expect(fetcher.mock.calls.map((call) => call[0])).toEqual(["/node-install/manifest.json"]);
  });
  it("preserves saved resources and Runtime only for the same provider", () => {
    const current: SandboxSpecification = { resources: { cpus: 7, memory_mib: 8192 }, runtime: { source_commit: manifest.source_commit,
      image_id: manifest.images.runtime, image_manifest_digest: manifest.image_manifest_digests.runtime,
      microsandbox_ref: manifest.runtime_ref, runtime_sha256: manifest.microsandbox.runtime_sha256, firmware_sha256: manifest.microsandbox.firmware_sha256 } };
    expect(savedSpecification("docker", "docker", current)).toEqual(current);
    expect(savedSpecification("docker", "docker", current)).not.toBe(current);
    expect(savedSpecification("microsandbox", "docker", current)).toBeNull();
    expect(savedSpecification("e2b", "docker", current)).toBeNull();
    expect(savedSpecification("e2b", "e2b", { resources: current.resources })).toEqual({ resources: current.resources });
  });
  it("keeps the installer's Standard sizes structure", () => {
    expect(Object.keys(standardSizes).sort()).toEqual(["docker", "microsandbox"]);
    expect(Object.keys(standardSizes.docker).sort()).toEqual(["cpus", "memory_mib"]);
    expect(Object.keys(standardSizes.microsandbox).sort()).toEqual(["cpus", "environment_disk_mib", "memory_mib", "root_disk_mib"]);
    for (const provider of ["docker", "microsandbox"] as const) {
      for (const value of Object.values(standardSizes[provider])) expect(Number.isInteger(value) && value > 0).toBe(true);
      const resources = defaultSandboxResources(provider);
      expect(resources).toEqual(standardSizes[provider]);
      expect(resources).not.toBe(standardSizes[provider]);
      expect(validSandboxResources(provider, resources)).toBe(true);
    }
  });
  it("respects CPU, memory and supported disk bounds", () => {
    for (const provider of ["docker", "microsandbox", "e2b"] as const) expect(validSandboxResources(provider, defaultSandboxResources(provider))).toBe(true);
    expect(validSandboxResources("docker", { cpus: 0, memory_mib: 2048 })).toBe(false);
    expect(validSandboxResources("e2b", { cpus: 256, memory_mib: 2048 })).toBe(false);
    expect(validSandboxResources("docker", { cpus: 2, memory_mib: 511 })).toBe(false);
    expect(validSandboxResources("docker", { cpus: 2, memory_mib: 1048577 })).toBe(false);
    expect(validSandboxResources("e2b", { cpus: 2, memory_mib: 2048, root_disk_mib: 1024 })).toBe(false);
    expect(validSandboxResources("microsandbox", { cpus: 2, memory_mib: 2048, root_disk_mib: 1023, environment_disk_mib: 8192 })).toBe(false);
  });
  it("accepts any well-formed Runtime image name in the Runtime reference", async () => {
    const digest = "b".repeat(64);
    for (const runtime_ref of [`parsar-core-runtime@sha256:${digest}`, `oac-runtime@sha256:${digest}`]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...manifest, runtime_ref }))));
      expect((await distributionRuntime(new AbortController().signal)).microsandbox_ref).toBe(runtime_ref);
      expect(isRuntimeReleaseField("microsandbox_ref", runtime_ref)).toBe(true);
    }
    for (const runtime_ref of [`oac-runtime:${digest}`, `oac-runtime@sha256:${"b".repeat(63)}`, `oac-runtime@sha256:${"B".repeat(64)}`, `@sha256:${digest}`]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...manifest, runtime_ref }))));
      await expect(distributionRuntime(new AbortController().signal)).rejects.toThrow();
      expect(isRuntimeReleaseField("microsandbox_ref", runtime_ref)).toBe(false);
    }
  });
  it("rejects unavailable, mutable or incomplete release identities", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 404 })));
    await expect(distributionRuntime(new AbortController().signal)).rejects.toThrow();
    for (const invalid of [{ ...manifest, platform: "linux/arm64" }, { ...manifest, source_commit: "main" }, { ...manifest, runtime_ref: "parsar-core-runtime:latest" }, { ...manifest, microsandbox: {} }]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(invalid))));
      await expect(distributionRuntime(new AbortController().signal)).rejects.toThrow();
    }
  });
});
