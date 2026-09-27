import type { CoreInstallation } from "@agents-core-web/agents-client";

import { isValidDirectCoreBaseUrl } from "../../lib/connection";

/**
 * The value itself when it is an HTTPS origin: no path, query, fragment or
 * credentials; a single trailing slash is dropped. Otherwise null. It is kept
 * as written, not normalized, so an explicit port such as :443 stays exactly
 * as Core reports it.
 */
export function httpsOrigin(value: string): string | null {
  const candidate = value.trim().replace(/\/$/, "");
  return /^https:\/\/[^/?#\\\s@]+$/i.test(candidate) && isValidDirectCoreBaseUrl(candidate) ? candidate : null;
}

/**
 * Where the node commands download the installer, and the `--source-url` they
 * pass it: the installation's public URL, whose reverse proxy sends
 * `/node-install/*` to this console. Unlike the browser's address, it is the
 * same from every machine. Null when other machines can't use it: loopback
 * (`local_only`), missing, or not an HTTPS origin.
 */
export function nodeSourceUrl(installation: Pick<CoreInstallation, "public_url" | "local_only">): string | null {
  if (installation.local_only || !installation.public_url) return null;
  return httpsOrigin(installation.public_url);
}
