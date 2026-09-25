import type { SandboxProvider, SandboxResources, SandboxRuntimeRelease, SandboxSpecification } from "@agents-core-web/agents-client";

interface Manifest {
  platform?: string;
  source_commit?: string;
  images?: { runtime?: string };
  image_manifest_digests?: { runtime?: string };
  runtime_ref?: string;
  microsandbox?: { runtime_sha256?: string; firmware_sha256?: string };
}

export function defaultSandboxResources(provider: SandboxProvider): SandboxResources {
  return provider === "microsandbox"
    ? { cpus: 2, memory_mib: 4096, root_disk_mib: 8192, environment_disk_mib: 8192 }
    : { cpus: 2, memory_mib: 2048 };
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

/** The paired console serves one matched distribution; Core persists approval. */
export async function distributionRuntime(signal: AbortSignal): Promise<SandboxRuntimeRelease> {
  const response = await fetch("/node-install/manifest.json", { signal, credentials: "include", redirect: "error" });
  if (!response.ok) throw new Error("distribution unavailable");
  const manifest = await response.json() as Manifest;
  const hash = /^[a-f0-9]{64}$/;
  const image = /^sha256:[a-f0-9]{64}$/;
  if (manifest.platform !== "linux/amd64" || !/^[a-f0-9]{40}$/.test(manifest.source_commit ?? "")
    || !image.test(manifest.images?.runtime ?? "") || !image.test(manifest.image_manifest_digests?.runtime ?? "")
    || !/^parsar-core-runtime@sha256:[a-f0-9]{64}$/.test(manifest.runtime_ref ?? "")
    || !hash.test(manifest.microsandbox?.runtime_sha256 ?? "") || !hash.test(manifest.microsandbox?.firmware_sha256 ?? "")) throw new Error("invalid distribution");
  return { source_commit: manifest.source_commit!, image_id: manifest.images!.runtime!, image_manifest_digest: manifest.image_manifest_digests!.runtime!,
    microsandbox_ref: manifest.runtime_ref!, runtime_sha256: manifest.microsandbox!.runtime_sha256!, firmware_sha256: manifest.microsandbox!.firmware_sha256! };
}
