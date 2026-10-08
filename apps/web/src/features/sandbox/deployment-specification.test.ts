import { describe, expect, it } from "vitest";
import { defaultSandboxResources, sandboxesThatFit, savedSpecification, validSandboxResources } from "./deployment-specification";
import { deploymentContract, type SandboxProvider, type SandboxSpecification } from "@oac/agents-client";


const manifest = { platform: "linux/amd64", source_commit: "0".repeat(40), images: { runtime: `sha256:${"a".repeat(64)}` },
  image_manifest_digests: { runtime: `sha256:${"b".repeat(64)}` }, runtime_ref: `oac-runtime@sha256:${"b".repeat(64)}`,
  microsandbox: { runtime_sha256: "c".repeat(64), firmware_sha256: "d".repeat(64) } };

describe("deployment resources and Runtime", () => {
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
