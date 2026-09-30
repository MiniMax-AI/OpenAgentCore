import type { SandboxRuntimeRelease } from "@oac/agents-client";

/** Core owns the Runtime image name, so the Web checks only the `<name>@sha256:<digest>` shape. */
export const RUNTIME_REF_PATTERN = /^[a-z0-9][a-z0-9._-]*@sha256:[0-9a-f]{64}$/;

const patterns: Record<keyof SandboxRuntimeRelease, RegExp> = {
  source_commit: /^[0-9a-f]{40}$/,
  image_id: /^sha256:[0-9a-f]{64}$/,
  image_manifest_digest: /^sha256:[0-9a-f]{64}$/,
  microsandbox_ref: RUNTIME_REF_PATTERN,
  runtime_sha256: /^[0-9a-f]{64}$/,
  firmware_sha256: /^[0-9a-f]{64}$/,
};

export const RUNTIME_RELEASE_FIELDS = Object.keys(patterns) as (keyof SandboxRuntimeRelease)[];

export function isRuntimeReleaseField(field: keyof SandboxRuntimeRelease, value: string): boolean {
  return patterns[field].test(value);
}

export function isRuntimeRelease(value: Partial<SandboxRuntimeRelease>): value is SandboxRuntimeRelease {
  return RUNTIME_RELEASE_FIELDS.every((field) => typeof value[field] === "string" && patterns[field].test(value[field]!));
}
