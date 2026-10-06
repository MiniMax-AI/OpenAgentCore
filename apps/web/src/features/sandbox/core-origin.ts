import type { CoreInstallation } from "@oac/agents-client";

/**
 * Where the node commands download the installer, and the `--source-url` they
 * pass it: the installation's public URL, whose reverse proxy sends
 * `/node-install/*` to this console. Unlike the browser's address, it is the
 * same from every machine. Core accepts only origins nodes may use, so it is
 * null only when other machines can't reach it (`local_only`) or it is missing.
 */
export function nodeSourceUrl(installation: Pick<CoreInstallation, "public_url" | "local_only">): string | null {
  if (installation.local_only) return null;
  return installation.public_url;
}
