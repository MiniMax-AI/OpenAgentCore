import { deploymentContract, type SandboxDeployment, type SandboxE2BTemplateBuild, type SandboxProvider, type SandboxResources, type SandboxRuntimeRelease, type SandboxSpecification } from "@oac/agents-client";
import standardSizes from "./standard-sizes.json";

interface Manifest {
  platform?: string;
  source_commit?: string;
  images?: { runtime?: string };
  image_manifest_digests?: { runtime?: string };
  runtime_ref?: string;
  microsandbox?: { runtime_sha256?: string; firmware_sha256?: string };
}

export function defaultSandboxResources(provider: SandboxProvider): SandboxResources {
  return { ...(provider === "microsandbox" ? standardSizes.microsandbox : standardSizes.docker) };
}

/** Core's resource rule: optional disk fields stay zero unless the provider supports disk limits. */
export function validSandboxResources(provider: SandboxProvider, resources: SandboxResources): boolean {
  return deploymentContract.resources.every((rule) => {
    const [min, max] = !rule.omit_zero ? [rule.min, rule.max] : provider === "microsandbox" ? [deploymentContract.minimum_disk, rule.max] : [0, 0];
    const value = resources[rule.name] ?? 0;
    return Number.isInteger(value) && value >= min && value <= max;
  });
}

const releasePatterns = Object.fromEntries(deploymentContract.runtime.map(({ name, pattern }) => [name, new RegExp(`^(?:${pattern})$`)])) as Record<keyof SandboxRuntimeRelease, RegExp>;

export const RUNTIME_RELEASE_FIELDS = deploymentContract.runtime.map(({ name }) => name);

export function isRuntimeReleaseField(field: keyof SandboxRuntimeRelease, value: string): boolean {
  return releasePatterns[field].test(value);
}

export function isRuntimeRelease(value: Partial<SandboxRuntimeRelease>): value is SandboxRuntimeRelease {
  return RUNTIME_RELEASE_FIELDS.every((field) => typeof value[field] === "string" && releasePatterns[field].test(value[field]));
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
export async function distributionRuntime(signal: AbortSignal): Promise<SandboxRuntimeRelease> {
  const response = await fetch("/node-install/manifest.json", { signal, credentials: "include", redirect: "error" });
  if (!response.ok) throw new Error("distribution unavailable");
  const manifest = await response.json() as Manifest;
  const release = { source_commit: manifest.source_commit, image_id: manifest.images?.runtime, image_manifest_digest: manifest.image_manifest_digests?.runtime,
    microsandbox_ref: manifest.runtime_ref, runtime_sha256: manifest.microsandbox?.runtime_sha256, firmware_sha256: manifest.microsandbox?.firmware_sha256 };
  if (manifest.platform !== "linux/amd64" || !isRuntimeRelease(release)) throw new Error("invalid distribution");
  return release;
}
