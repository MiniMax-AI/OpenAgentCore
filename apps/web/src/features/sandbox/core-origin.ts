import { isLoopbackHostname, isValidDirectCoreBaseUrl } from "../../lib/connection";

export function sandboxCoreOrigin(value: string): string | null {
  const candidate = value.trim();
  if (!/^https?:\/\/[^/?#\\\s]+\/?$/i.test(candidate) || !isValidDirectCoreBaseUrl(candidate)) return null;
  const url = new URL(candidate);
  if (url.protocol === "http:" && url.hostname.endsWith(".localhost")) return null;
  return url.origin;
}

export function sandboxSetupOrigin(value: string): string | null {
  const origin = sandboxCoreOrigin(value);
  if (!origin) return null;
  const url = new URL(origin);
  const hostname = url.hostname.replace(/\.$/, "");
  const mappedIPv4 = /^\[::ffff:([0-9a-f]+):([0-9a-f]+)\]$/.exec(hostname);
  const mappedHigh = mappedIPv4 ? Number.parseInt(mappedIPv4[1] ?? "", 16) : null;
  const mappedLow = mappedIPv4 ? Number.parseInt(mappedIPv4[2] ?? "", 16) : null;
  const unusable = isLoopbackHostname(hostname) || hostname === "0.0.0.0" || hostname === "[::]"
    || (mappedHigh !== null && (mappedHigh >>> 8 === 127 || (mappedHigh === 0 && mappedLow === 0)));
  return url.protocol === "https:" && !unusable ? origin : null;
}
