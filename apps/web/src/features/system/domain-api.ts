import { queryOptions } from "@tanstack/react-query";

export interface DomainStatus {
  supported: boolean;
  state: "unconfigured" | "checking" | "applying" | "ready" | "failed";
  public_url: string | null;
  target_url: string | null;
  message: string | null;
}

export class DomainRequestError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) { super(message); }
}

/** A hostname, never a URL, port, IP address or path. IDNs use their ASCII form. */
export function domainHostname(input: string): string | null {
  const text = input.trim();
  if (!text || /[\s/:?#@\\]/u.test(text)) return null;
  try {
    const host = new URL(`https://${text}`).hostname;
    const labels = host.split(".");
    return host.length <= 253 && labels.length > 1 && !/^\d+$/.test(labels.at(-1)!) &&
      labels.every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label)) ? host : null;
  } catch { return null; }
}

/** Only a canonical HTTPS domain can become the post-apply navigation link. */
export function domainTarget(value: string | null): string | null {
  if (!value?.startsWith("https://")) return null;
  const host = domainHostname(value.slice(8));
  return host && value === `https://${host}` ? value : null;
}

export function domainInProgress(status?: DomainStatus): boolean {
  return status?.state === "checking" || status?.state === "applying";
}

function parseStatus(value: unknown): DomainStatus {
  if (!value || typeof value !== "object") throw new DomainRequestError(502, "invalid_response", "Invalid domain settings response.");
  const data = value as Record<string, unknown>;
  if (typeof data.supported !== "boolean" || !["unconfigured", "checking", "applying", "ready", "failed"].includes(String(data.state)) ||
    ![data.public_url, data.target_url, data.message].every((entry) => entry === null || typeof entry === "string") ||
    (data.target_url !== null && !domainTarget(data.target_url as string))) {
    throw new DomainRequestError(502, "invalid_response", "Invalid domain settings response.");
  }
  return { supported: data.supported, state: data.state as DomainStatus["state"], public_url: data.public_url as string | null,
    target_url: data.target_url as string | null, message: data.message as string | null };
}

async function domainRequest(init: RequestInit): Promise<DomainStatus> {
  const response = await fetch("/console/installation/domain", { credentials: "same-origin", cache: "no-store", ...init });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new DomainRequestError(response.status, typeof body?.error?.code === "string" ? body.error.code : "request_failed",
      typeof body?.error?.message === "string" ? body.error.message : "Domain settings could not be read.");
  }
  return parseStatus(await response.json());
}

export const domainQuery = queryOptions({
  queryKey: ["console-domain"],
  queryFn: ({ signal }) => domainRequest({ signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]) }),
  retry: false,
  refetchInterval: (query) => domainInProgress(query.state.data) && !query.state.error ? 2_000 : false,
});

/** One same-origin write per user action; never retries a possibly accepted apply. */
export function applyDomain(hostname: string, confirmed: boolean, signal: AbortSignal): Promise<DomainStatus> {
  return domainRequest({ method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ hostname, ...(confirmed ? { confirm_public_url_change: `https://${hostname}` } : {}) }), signal });
}
