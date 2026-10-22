import { deploymentContract, type SandboxDeployment, type SandboxE2BTemplateBuild, type SandboxProvider, type SandboxResources, type SandboxSpecification } from "@oac/agents-client";

/** The size the Provider declares for setup to propose; null when its configuration selects the size. */
export function defaultSandboxResources(provider: SandboxProvider): SandboxResources | null {
  const size: SandboxResources | null = deploymentContract.providers[provider].default_resources;
  return size && { ...size };
}

/** The workspace requirement a Provider takes from the selected storage; none when it owns its workspaces or no storage is selected. */
export function selectedWorkspace(provider: SandboxProvider, storage: { tenant_capacity_mib: number } | null | undefined): SandboxSpecification["workspace"] {
  const policy = deploymentContract.providers[provider];
  return storage && "workspace" in policy ? { ...policy.workspace, capacity_quota: storage.tenant_capacity_mib > 0 } : undefined;
}

/** A Provider that declares a workspace policy runs every Environment on the selected storage; its environment disk is only a quota request. */
export function validSandboxResources(provider: SandboxProvider, resources: SandboxResources, workspace?: SandboxSpecification["workspace"]): boolean {
  const policy = deploymentContract.providers[provider];
  if ("workspace" in policy !== Boolean(workspace)) return false;
  if (workspace && "workspace" in policy && (workspace.attachment !== policy.workspace.attachment || (policy.workspace.user_xattr && !workspace.user_xattr))) return false;
  return deploymentContract.resources.every((rule) => {
    const value = resources[rule.name] ?? 0;
    const [min, max] = !rule.omit_zero ? [rule.min, rule.max] : rule.name === "environment_disk_mib" && workspace?.capacity_quota ? [value === 0 ? 0 : deploymentContract.minimum_disk, rule.max] : rule.name === "root_disk_mib" && policy.disk ? [deploymentContract.minimum_disk, rule.max] : [0, 0];
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
