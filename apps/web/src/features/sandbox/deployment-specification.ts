import type { SandboxDeployment, SandboxE2BTemplateBuild, SandboxProvider, SandboxResources, SandboxRuntimeRelease, SandboxSpecification } from "@agents-core-web/agents-client";
import { RUNTIME_REF_PATTERN } from "./runtime-release";
import standardSizes from "./standard-sizes.json";

interface Manifest {
  platform?: string;
  source_commit?: string;
  images?: { runtime?: string };
  image_manifest_digests?: { runtime?: string };
  runtime_ref?: string;
  microsandbox?: { runtime_sha256?: string; firmware_sha256?: string };
}

// The Standard sizes are a contract with the Core installer (install.sh); see standard-sizes.md before changing their structure.
export function defaultSandboxResources(provider: SandboxProvider): SandboxResources {
  return { ...(provider === "microsandbox" ? standardSizes.microsandbox : standardSizes.docker) };
}

export function validSandboxResources(provider: SandboxProvider, resources: SandboxResources): boolean {
  const bounded = (value: number | undefined, minimum: number, maximum: number) => Number.isInteger(value) && value! >= minimum && value! <= maximum;
  if (!bounded(resources.cpus, 1, 255) || !bounded(resources.memory_mib, 512, 1048576)) return false;
  return provider === "microsandbox"
    ? bounded(resources.root_disk_mib, 1024, 4294967295) && bounded(resources.environment_disk_mib, 1024, 4294967295)
    : (resources.root_disk_mib ?? 0) === 0 && (resources.environment_disk_mib ?? 0) === 0;
}

export function savedSpecification(provider: SandboxProvider, savedProvider?: SandboxProvider | "", specification?: SandboxSpecification): SandboxSpecification | null {
  return provider === savedProvider && specification ? structuredClone(specification) : null;
}

/** CPU and memory of the E2B template build as Core read them when the selection was saved; null while either is unknown. */
export function templateBuildSize(deployment: SandboxDeployment): { cpus: number; memory_mib: number } | null {
  const build = deployment.e2b?.template_build?.resources;
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

/** The paired console serves one matched distribution; Core persists approval. */
export async function distributionRuntime(signal: AbortSignal): Promise<SandboxRuntimeRelease> {
  const response = await fetch("/node-install/manifest.json", { signal, credentials: "include", redirect: "error" });
  if (!response.ok) throw new Error("distribution unavailable");
  const manifest = await response.json() as Manifest;
  const hash = /^[a-f0-9]{64}$/;
  const image = /^sha256:[a-f0-9]{64}$/;
  if (manifest.platform !== "linux/amd64" || !/^[a-f0-9]{40}$/.test(manifest.source_commit ?? "")
    || !image.test(manifest.images?.runtime ?? "") || !image.test(manifest.image_manifest_digests?.runtime ?? "")
    || !RUNTIME_REF_PATTERN.test(manifest.runtime_ref ?? "")
    || !hash.test(manifest.microsandbox?.runtime_sha256 ?? "") || !hash.test(manifest.microsandbox?.firmware_sha256 ?? "")) throw new Error("invalid distribution");
  return { source_commit: manifest.source_commit!, image_id: manifest.images!.runtime!, image_manifest_digest: manifest.image_manifest_digests!.runtime!,
    microsandbox_ref: manifest.runtime_ref!, runtime_sha256: manifest.microsandbox!.runtime_sha256!, firmware_sha256: manifest.microsandbox!.firmware_sha256! };
}
