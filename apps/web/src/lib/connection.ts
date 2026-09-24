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

export function isValidDirectCoreBaseUrl(value: string): boolean {
  try {
    const candidate = value.trim();
    if (candidate.includes("?") || candidate.includes("#") || hasExplicitUserInfo(candidate)) return false;
    const url = new URL(candidate);
    const secureTransport = url.protocol === "https:" || (url.protocol === "http:" && isLoopbackHostname(url.hostname));
    return secureTransport && !url.username && !url.password && !url.search && !url.hash;
  } catch {
    return false;
  }
}
