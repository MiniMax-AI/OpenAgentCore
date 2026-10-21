import { deploymentContract, type SandboxDeployment, type SandboxE2BTemplateBuild, type SandboxProvider, type SandboxResources, type SandboxSpecification } from "@oac/agents-client";

/** The size the Provider declares for setup to propose; null when its configuration selects the size. */
export function defaultSandboxResources(provider: SandboxProvider, workspace?: SandboxSpecification["workspace"]): SandboxResources | null {
  const size: SandboxResources | null = deploymentContract.providers[provider].default_resources;
  return size && { ...size, ...(workspace && !workspace.capacity_quota ? { environment_disk_mib: 0 } : {}) };
}

/** The workspace requirement a Provider takes from the selected storage; none when it owns its workspaces or no storage is selected. */
export function selectedWorkspace(provider: SandboxProvider, storage: { tenant_capacity_mib: number } | null | undefined): SandboxSpecification["workspace"] {
  const policy = deploymentContract.providers[provider];
  return storage && "workspace" in policy ? { ...policy.workspace, capacity_quota: storage.tenant_capacity_mib > 0 } : undefined;
}

/** Core keeps owned disk limits; external workspace capacity is optional and requires real quota support. */
export function validSandboxResources(provider: SandboxProvider, resources: SandboxResources, workspace?: SandboxSpecification["workspace"]): boolean {
  const policy = deploymentContract.providers[provider];
  if (workspace) {
    if (!("workspace" in policy) || workspace.attachment !== policy.workspace.attachment || (policy.workspace.user_xattr && !workspace.user_xattr) || ((resources.environment_disk_mib ?? 0) > 0 && !workspace.capacity_quota)) return false;
  }
  return deploymentContract.resources.every((rule) => {
    const [min, max] = workspace && rule.name === "environment_disk_mib" && (resources.environment_disk_mib ?? 0) === 0 ? [0, 0] : !rule.omit_zero ? [rule.min, rule.max] : deploymentContract.providers[provider].disk ? [deploymentContract.minimum_disk, rule.max] : [0, 0];
    const value = resources[rule.name] ?? 0;
    return Number.isInteger(value) && value >= min && value <= max;
  });
}

export function savedSpecification(provider: SandboxProvider, savedProvider?: SandboxProvider | "", specification?: SandboxSpecification): SandboxSpecification | null {
  return provider === savedProvider && specification ? structuredClone(specification) : null;
}

/** CPU and memory of the E2B template build as Core read them when the selection was saved; null while either is unknown. */
export function templateBuildSize(deployment: SandboxDeployment): { cpus: number; memory_mib: number } | null {
  const build = deployment.metadata?.template_build?.resources;
  return build && build.cpus !== null && build.memory_mib !== null ? { cpus: build.cpus, memory_mib: build.memory_mib } : null;
}

/** Core admits only a ready build; a selection saved before Core recorded the build has no status. */
export function templateBuildStatus(build: SandboxE2BTemplateBuild | undefined): "ready" | "notReady" | "unknown" {
  if (!build?.status) return "unknown";
  return build.status === "ready" ? "ready" : "notReady";
}

/** Each sandbox's limits: the saved specification, else the E2B template build that an E2B selection adopts. */
export function sandboxSize(deployment: SandboxDeployment): SandboxResources | null {
  return deployment.specification?.resources ?? templateBuildSize(deployment);
}
