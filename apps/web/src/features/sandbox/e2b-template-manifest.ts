/** Projection of deploy/e2b/template_manifest.py, verified by shared fixtures. */
export const MAX_MANIFEST_BYTES = 16384;
export interface E2BTemplateManifest {
  api_url: string;
  domain: string;
  template: string;
  image: string;
  runtime_sha256: string;
  base: string;
}

// Core accepts a template ID of up to 128 characters and a canonical, non-nil build UUID.
const TEMPLATE = /^[a-zA-Z0-9_-]{1,128}:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
export function validTemplate(value: string): boolean {
  const build = TEMPLATE.exec(value)?.[1];
  return value === value.trim() && build !== undefined && /[^0-]/.test(build);
}

export function validEndpoint(apiURL: string, domain: string): boolean {
  const publicName = (host: string) => host === host.trim() && host.length <= 253 && host.includes(".") && !/^[0-9.]+$/.test(host) &&
    !host.endsWith(".local") && !host.endsWith(".localhost") &&
    host.split(".").every((label) => label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label));
  if (!apiURL && !domain) return true;
  if (!apiURL || !domain || apiURL.length > 512 || !publicName(domain)) return false;
  try {
    const url = new URL(apiURL);
    return url.protocol === "https:" && url.origin === apiURL && !url.username && !url.password &&
      !url.port && publicName(url.hostname) && (url.hostname === domain || url.hostname.endsWith(`.${domain}`));
  } catch {
    return false;
  }
}

/** Local input only. Import does not authenticate or qualify a Runtime. */
export function parseTemplateManifest(text: string): E2BTemplateManifest {
  if (new TextEncoder().encode(text).length > MAX_MANIFEST_BYTES) throw new Error("Invalid template build file");
  const value: unknown = JSON.parse(text);
  const fields = ["api_url", "domain", "template", "image", "runtime_sha256", "base"];
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).length !== fields.length ||
      !Object.entries(value).every(([key, item]) => fields.includes(key) && typeof item === "string" && item === item.trim())) {
    throw new Error("Invalid template build file");
  }
  const manifest = value as E2BTemplateManifest;
  if (!manifest.api_url || !manifest.domain || !validEndpoint(manifest.api_url, manifest.domain) ||
      !validTemplate(manifest.template) || !/^sha256:[0-9a-f]{64}$/.test(manifest.image) ||
      !/^[0-9a-f]{64}$/.test(manifest.runtime_sha256) || manifest.base.length > 512 ||
      !/^[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64}$/.test(manifest.base)) {
    throw new Error("Invalid template build file");
  }
  return manifest;
}
