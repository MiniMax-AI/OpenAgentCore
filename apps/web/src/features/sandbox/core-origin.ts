import type { CoreInstallation } from "@agents-core-web/agents-client";

import { isValidDirectCoreBaseUrl } from "../../lib/connection";

export function sandboxCoreOrigin(value: string): string | null {
  const candidate = value.trim();
  if (!/^https?:\/\/[^/?#\\\s]+\/?$/i.test(candidate) || !isValidDirectCoreBaseUrl(candidate)) return null;
  const url = new URL(candidate);
  if (url.protocol === "http:" && url.hostname.endsWith(".localhost")) return null;
  return url.origin;
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
  return sandboxCoreOrigin(installation.public_url);
}
