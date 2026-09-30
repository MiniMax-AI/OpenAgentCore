const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

/**
 * The start the node commands share: a private directory removed on exit, then
 * the console's installer, checked against its digest before anything runs. The
 * leading space keeps the command out of shell history under
 * HISTCONTROL=ignorespace. In sudo mode `s` is set before the `&&` chain, so a
 * failed download or check stops the command. It ends where the installer's own
 * line begins.
 */
function nodeInstaller(sourceUrl: string, scriptDigest: string): string {
  return ` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
export http_proxy="\${http_proxy-\${HTTP_PROXY-}}" https_proxy="\${https_proxy-\${HTTPS_PROXY-}}" no_proxy="\${no_proxy-\${NO_PROXY-}}"
export HTTP_PROXY="$http_proxy" HTTPS_PROXY="$https_proxy" NO_PROXY="$no_proxy"
printf '\\n==> Downloading node installer...\\n' &&
curl -fs --max-time 30 --max-filesize 1048576 ${quote(sourceUrl + "/node-install/node-install.pyz")} -o "$d/node-install.pyz" || { c=$?; printf 'Cannot download node installer; check the console URL, TLS and proxy settings.\\n' >&2; exit "$c"; }
printf '==> Verifying node installer...\\n' &&
printf '%s  %s\\n' ${quote(scriptDigest)} "$d/node-install.pyz" | sha256sum -c --status &&
`;
}

/** Runs the downloaded installer, as root in sudo mode. */
const runInstaller = `$s \${s:+--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY} python3 "$d/node-install.pyz" \${NO_COLOR+--no-color}`;

/**
 * Adds this host as a node. The one-time token reaches the installer only on
 * standard input (`printf` is a shell builtin), never in an argument, the
 * environment or sudo's command line.
 */
export function nodeInstallCommand({ token, coreUrl, sourceUrl, provider, installationId, scriptDigest }: {
  token: string; coreUrl: string; sourceUrl: string; provider: "docker" | "microsandbox"; installationId: string; scriptDigest: string;
}): string {
  return `${nodeInstaller(sourceUrl, scriptDigest)}printf '%s\\n' ${quote(token)} | ${runInstaller} --enrollment-token-stdin --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)})`;
}

/**
 * Removes a node Core no longer lists from its host: its service, its files and,
 * when no node uses it, the service user. It holds no secret. The installer first
 * confirms with Core, at the address the node enrolled with, that the node is
 * removed; `force` skips that check, for an address that no longer answers.
 */
export function nodeUninstallCommand({ sourceUrl, installationId, scriptDigest, force = false }: { sourceUrl: string; installationId: string; scriptDigest: string; force?: boolean }): string {
  return `${nodeInstaller(sourceUrl, scriptDigest)}${runInstaller} --uninstall --installation-id ${quote(installationId)}${force ? " --force" : ""})`;
}

/**
 * The node service's journal. The installer names the unit after the
 * installation (node_install.py `unit_name`), always a system unit.
 */
export function nodeLogCommand(installationId: string): string {
  const unit = `oac-node-${installationId}.service`;
  return `sudo journalctl -u ${/^[A-Za-z0-9._-]+$/.test(unit) ? unit : quote(unit)}`;
}
