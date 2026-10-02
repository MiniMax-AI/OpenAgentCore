/**
 * URL checks shared by the sandbox pages. The console itself calls only the
 * same-origin Web API; it keeps no Core connection or token in the browser.
 */
export type CoreConnectionState = "connecting" | "ready" | "failed";

export function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase();
  return (
    normalized === "localhost" ||
    normalized.endsWith(".localhost") ||
    normalized === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(normalized)
  );
}

function hasExplicitUserInfo(candidate: string): boolean {
  const schemeEnd = candidate.indexOf(":");
  if (schemeEnd < 0) return false;
  const remainder = candidate.slice(schemeEnd + 1).replace(/^[\\/]+/, "");
  const authorityEnd = remainder.search(/[\\/?#]/);
  const authority = authorityEnd < 0 ? remainder : remainder.slice(0, authorityEnd);
  return authority.includes("@");
}

/**
 * Whether the value is a Core origin a direct caller may use. Plain HTTP qualifies only for a
 * loopback host, or when the caller says the installation opted into a trusted network; that
 * decision stays with the caller.
 */
export function isValidDirectCoreBaseUrl(value: string, allowInsecureHTTP = false): boolean {
  try {
    const candidate = value.trim();
    if (candidate.includes("?") || candidate.includes("#") || hasExplicitUserInfo(candidate)) return false;
    const url = new URL(candidate);
    const secureTransport = url.protocol === "https:" || (url.protocol === "http:" && (allowInsecureHTTP || isLoopbackHostname(url.hostname)));
    return secureTransport && !url.username && !url.password && !url.search && !url.hash;
  } catch {
    return false;
  }
}
