import { deploymentContract, type SandboxDeployment, type SandboxE2BTemplateBuild, type SandboxProvider, type SandboxResources, type SandboxRuntimeRelease, type SandboxSpecification } from "@oac/agents-client";

/** The size the Provider declares for setup to propose; null when its configuration selects the size. */
export function defaultSandboxResources(provider: SandboxProvider): SandboxResources | null {
  const size: SandboxResources | null = deploymentContract.providers[provider].default_resources;
  return size && { ...size };
}

/** Core's resource rule: optional disk fields stay zero unless the Provider declares disk limits. */
export function validSandboxResources(provider: SandboxProvider, resources: SandboxResources): boolean {
  return deploymentContract.resources.every((rule) => {
    const [min, max] = !rule.omit_zero ? [rule.min, rule.max] : deploymentContract.providers[provider].disk ? [deploymentContract.minimum_disk, rule.max] : [0, 0];
    const value = resources[rule.name] ?? 0;
    return Number.isInteger(value) && value >= min && value <= max;
  });
}

export function runtimeReleaseFields(provider: SandboxProvider): string[] {
  return ["source_commit", ...Object.keys(deploymentContract.providers[provider].artifacts)];
}

export function isRuntimeReleaseField(provider: SandboxProvider, field: string, value: string): boolean {
  const rules: Record<string, { pattern: string }> = deploymentContract.providers[provider].artifacts;
  const pattern = field === "source_commit" ? deploymentContract.source_commit_pattern : rules[field]?.pattern;
  return pattern !== undefined && new RegExp(`^(?:${pattern})(?![\\s\\S])`).test(value);
}

export function isRuntimeRelease(provider: SandboxProvider, value: Partial<SandboxRuntimeRelease>): value is SandboxRuntimeRelease {
  const fields = Object.keys(deploymentContract.providers[provider].artifacts);
  return fields.length > 0 && typeof value.source_commit === "string" && isRuntimeReleaseField(provider, "source_commit", value.source_commit)
    && value.artifacts !== null && typeof value.artifacts === "object" && !Array.isArray(value.artifacts)
    && Object.keys(value).every((key) => key === "source_commit" || key === "artifacts")
    && Object.keys(value.artifacts).length === fields.length
    && fields.every((field) => typeof value.artifacts?.[field] === "string" && isRuntimeReleaseField(provider, field, value.artifacts[field]));
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

/**
 * At most how many sandboxes of this size a host's CPUs and memory hold at once,
 * each at its full limits; null while a figure or the size is unknown. A
 * suggestion for a node's limit, which Core itself never derives.
 */
export function sandboxesThatFit(host: { cpus: number | null; memoryBytes: number | null }, size: Pick<SandboxResources, "cpus" | "memory_mib"> | null): number | null {
  if (!size || host.cpus === null || host.memoryBytes === null || size.cpus <= 0 || size.memory_mib <= 0) return null;
  return Math.min(Math.floor(host.cpus / size.cpus), Math.floor(host.memoryBytes / (size.memory_mib * 2 ** 20)));
}

/** The paired console serves one matched distribution; Core persists approval. */
export async function distributionRuntime(provider: SandboxProvider, signal: AbortSignal): Promise<SandboxRuntimeRelease> {
  const response = await fetch("/node-install/manifest.json", { signal, credentials: "include", redirect: "error" });
  if (!response.ok) throw new Error("distribution unavailable");
  const manifest: unknown = await response.json();
  const at = (path: readonly string[]): unknown => path.reduce<unknown>((value, key) =>
    value !== null && typeof value === "object" && !Array.isArray(value) && Object.hasOwn(value, key) ? (value as Record<string, unknown>)[key] : undefined, manifest);
  const artifacts = Object.fromEntries(Object.entries(deploymentContract.providers[provider].artifacts).map(([name, rule]) => [name, at(rule.manifest_path)]));
  const release = { source_commit: at(["source_commit"]), artifacts };
  if (at(["platform"]) !== "linux/amd64" || !isRuntimeRelease(provider, release as Partial<SandboxRuntimeRelease>)) throw new Error("invalid distribution");
  return release as SandboxRuntimeRelease;
}
