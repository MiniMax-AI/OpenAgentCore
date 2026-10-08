import { afterEach, describe, expect, it, vi } from "vitest";
import { defaultSandboxResources, distributionRuntime, isRuntimeRelease, isRuntimeReleaseField, sandboxesThatFit, savedSpecification, validSandboxResources } from "./deployment-specification";
import { deploymentContract, type SandboxProvider, type SandboxRuntimeRelease, type SandboxSpecification } from "@oac/agents-client";

import deploymentContractFixture from "../../../../../services/core/internal/sandbox/testdata/deployment-contract.json";

const manifest = { platform: "linux/amd64", source_commit: "0".repeat(40), images: { runtime: `sha256:${"a".repeat(64)}` },
  image_manifest_digests: { runtime: `sha256:${"b".repeat(64)}` }, runtime_ref: `oac-runtime@sha256:${"b".repeat(64)}`,
  microsandbox: { runtime_sha256: "c".repeat(64), firmware_sha256: "d".repeat(64) } };
afterEach(() => vi.unstubAllGlobals());

describe("deployment resources and Runtime", () => {
  it.each(deploymentContractFixture.filter((entry) => entry.provider !== "e2b" && (entry.valid || /release|artifact|newline|crlf/.test(entry.name))))("checks the shared release contract: $name", (entry) => {
    expect(isRuntimeRelease(entry.provider as SandboxProvider, entry.specification.runtime as unknown as Partial<SandboxRuntimeRelease>)).toBe(entry.valid);
  });
  it("maps only each provider's declared identities from one matched distribution", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(manifest))));
    vi.stubGlobal("fetch", fetcher);
    expect(await distributionRuntime("docker", new AbortController().signal)).toEqual({ source_commit: manifest.source_commit,
      artifacts: { image_id: manifest.images.runtime, image_manifest_digest: manifest.image_manifest_digests.runtime } });
    expect(await distributionRuntime("microsandbox", new AbortController().signal)).toEqual({ source_commit: manifest.source_commit,
      artifacts: { microsandbox_ref: manifest.runtime_ref, runtime_sha256: manifest.microsandbox.runtime_sha256, firmware_sha256: manifest.microsandbox.firmware_sha256 } });
    await expect(distributionRuntime("e2b", new AbortController().signal)).rejects.toThrow();
    expect(fetcher.mock.calls.every((call) => call[0] === "/node-install/manifest.json")).toBe(true);
  });
  it("requires only the selected provider's manifest identities", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ platform: manifest.platform, source_commit: manifest.source_commit, images: manifest.images, image_manifest_digests: manifest.image_manifest_digests }))));
    const release = await distributionRuntime("docker", new AbortController().signal);
    expect(isRuntimeRelease("docker", release)).toBe(true);
    expect(isRuntimeRelease("microsandbox", release)).toBe(false);
    expect(isRuntimeRelease("docker", { ...release, artifacts: { ...release.artifacts, extra: "x" } })).toBe(false);
  });
  it("preserves saved resources and Runtime only for the same provider", () => {
    const current: SandboxSpecification = { resources: { cpus: 7, memory_mib: 8192 }, runtime: { source_commit: manifest.source_commit,
      artifacts: { image_id: manifest.images.runtime, image_manifest_digest: manifest.image_manifest_digests.runtime } } };
    expect(savedSpecification("docker", "docker", current)).toEqual(current);
    expect(savedSpecification("docker", "docker", current)).not.toBe(current);
    expect(savedSpecification("microsandbox", "docker", current)).toBeNull();
    expect(savedSpecification("e2b", "docker", current)).toBeNull();
    expect(savedSpecification("e2b", "e2b", { resources: current.resources })).toEqual({ resources: current.resources });
  });
  it("proposes a copy of each declared default size, which the declared bounds accept", () => {
    for (const provider of Object.keys(deploymentContract.providers) as SandboxProvider[]) {
      const declared = deploymentContract.providers[provider].default_resources;
      const resources = defaultSandboxResources(provider);
      expect(resources).toEqual(declared);
      if (resources) {
        expect(resources).not.toBe(declared);
        expect(validSandboxResources(provider, resources)).toBe(true);
      }
    }
  });
  it("respects CPU, memory and supported disk bounds", () => {
    expect(validSandboxResources("docker", { cpus: 0, memory_mib: 2048 })).toBe(false);
    expect(validSandboxResources("e2b", { cpus: 256, memory_mib: 2048 })).toBe(false);
    expect(validSandboxResources("docker", { cpus: 2, memory_mib: 511 })).toBe(false);
    expect(validSandboxResources("docker", { cpus: 2, memory_mib: 1048577 })).toBe(false);
    expect(validSandboxResources("e2b", { cpus: 2, memory_mib: 2048, root_disk_mib: 1024 })).toBe(false);
    expect(validSandboxResources("microsandbox", { cpus: 2, memory_mib: 2048, root_disk_mib: 1023, environment_disk_mib: 8192 })).toBe(false);
  });
  it("accepts only Core's Runtime image in the Runtime reference", async () => {
    const digest = "b".repeat(64);
    const runtime_ref = `oac-runtime@sha256:${digest}`;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...manifest, runtime_ref }))));
    expect((await distributionRuntime("microsandbox", new AbortController().signal)).artifacts.microsandbox_ref).toBe(runtime_ref);
    expect(isRuntimeReleaseField("microsandbox", "microsandbox_ref", runtime_ref)).toBe(true);
    for (const runtime_ref of [`custom-runtime@sha256:${digest}`, `oac-runtime:${digest}`, `oac-runtime@sha256:${"b".repeat(63)}`, `oac-runtime@sha256:${"B".repeat(64)}`, `@sha256:${digest}`, `oac-runtime@sha256:${digest}\n`]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...manifest, runtime_ref }))));
      await expect(distributionRuntime("microsandbox", new AbortController().signal)).rejects.toThrow();
      expect(isRuntimeReleaseField("microsandbox", "microsandbox_ref", runtime_ref)).toBe(false);
    }
  });
  it("rejects unavailable, mutable or incomplete release identities", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 404 })));
    await expect(distributionRuntime("microsandbox", new AbortController().signal)).rejects.toThrow();
    for (const invalid of [{ ...manifest, platform: "linux/arm64" }, { ...manifest, source_commit: "main" }, { ...manifest, source_commit: manifest.source_commit + "\n" }, { ...manifest, runtime_ref: "oac-runtime:latest" }, { ...manifest, microsandbox: {} }]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(invalid))));
      await expect(distributionRuntime("microsandbox", new AbortController().signal)).rejects.toThrow();
    }
  });
});

describe("sandboxes a host holds", () => {
  it("is the smaller of what its CPUs and its memory hold, and unknown without either or the size", () => {
    const size = { cpus: 2, memory_mib: 4096 };
    expect(sandboxesThatFit({ cpus: 16, memoryBytes: 64 * 2 ** 30 }, size)).toBe(8);
    expect(sandboxesThatFit({ cpus: 64, memoryBytes: 18 * 2 ** 30 }, size)).toBe(4);
    expect(sandboxesThatFit({ cpus: null, memoryBytes: 64 * 2 ** 30 }, size)).toBeNull();
    expect(sandboxesThatFit({ cpus: 16, memoryBytes: 64 * 2 ** 30 }, null)).toBeNull();
  });
});
