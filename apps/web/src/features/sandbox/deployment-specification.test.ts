import { describe, expect, it } from "vitest";
import { defaultSandboxResources, savedSpecification, selectedWorkspace, validSandboxResources } from "./deployment-specification";
import { deploymentContract, type SandboxProvider, type SandboxSpecification } from "@oac/agents-client";

describe("deployment resources", () => {
  it("preserves saved resources only for the same provider", () => {
    const current: SandboxSpecification = { resources: { cpus: 7, memory_mib: 8192 } };
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
  it("validates external storage without fabricating a disk quota or weakening root disk bounds", () => {
    const workspace = { ...deploymentContract.providers.microsandbox.workspace, capacity_quota: false };
    const resources = defaultSandboxResources("microsandbox", workspace)!;
    expect(resources.environment_disk_mib).toBe(0);
    expect(validSandboxResources("microsandbox", resources, workspace)).toBe(true);
    expect(validSandboxResources("microsandbox", resources)).toBe(false);
    expect(validSandboxResources("microsandbox", { ...resources, root_disk_mib: 0 }, workspace)).toBe(false);
    expect(validSandboxResources("microsandbox", { ...resources, environment_disk_mib: 8192 }, workspace)).toBe(false);
    expect(validSandboxResources("microsandbox", resources, { ...workspace, user_xattr: false })).toBe(false);
    expect(validSandboxResources("docker", { cpus: 2, memory_mib: 2048 }, workspace)).toBe(false);
    expect(validSandboxResources("microsandbox", { ...resources, environment_disk_mib: 8192 }, { ...workspace, capacity_quota: true })).toBe(true);
  });
  it("sizes a first initialization from the selected workspace storage", () => {
    const sized = (capacity: number | null) => {
      const workspace = selectedWorkspace("microsandbox", capacity === null ? null : { tenant_capacity_mib: capacity });
      const resources = defaultSandboxResources("microsandbox", workspace)!;
      return { workspace, resources, valid: validSandboxResources("microsandbox", resources, workspace) };
    };
    expect(sized(0)).toMatchObject({ workspace: { capacity_quota: false }, resources: { environment_disk_mib: 0 }, valid: true });
    expect(sized(1024)).toMatchObject({ workspace: { capacity_quota: true }, resources: { environment_disk_mib: 8192 }, valid: true });
    expect(sized(null).workspace).toBeUndefined();
    expect(selectedWorkspace("docker", { tenant_capacity_mib: 0 })).toBeUndefined();
  });
});
