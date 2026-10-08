import type { SandboxProvider } from "@oac/agents-client";

const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

/**
 * Download and verify into an immutable cache entry before invoking sudo.
 * The download lock protects disposable files; verified entries remain usable
 * by other running installers. A leading space supports HISTCONTROL=ignorespace.
 */
function nodeInstaller(sourceUrl: string, scriptDigest: string, sourceCommit: string): string {
  if (!/^[0-9a-f]{64}$/.test(scriptDigest)) throw new Error("Invalid node installer checksum");
  return ` (umask 077
[ "$(uname -s)/$(uname -m)" = Linux/x86_64 ] || { printf 'Node installation requires Linux amd64. Use a Linux host for Nodes, or a self-hosted Session for this machine.\\n' >&2; exit 1; }
for tool in curl sha256sum python3 flock; do command -v "$tool" >/dev/null || { printf 'Required command missing: %s. Install it and rerun.\\n' "$tool" >&2; exit 1; }; done
python3 -c 'import sys; sys.exit(sys.version_info < (3, 9))' || { printf 'Python 3.9 or newer is required.\\n' >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || command -v sudo >/dev/null || { printf 'sudo is required; ask the host administrator to run this command.\\n' >&2; exit 1; }
d="$HOME/.oac/node-bootstrap"
[ ! -L "$d" ] && mkdir -p "$d" && [ -d "$d" ] && [ -O "$d" ] && chmod 700 "$d" || { printf 'Cannot use the private node bootstrap directory.\\n' >&2; exit 1; }
installer="$d"/${quote(scriptDigest + ".pyz")}
for f in "$d/download.lock" "$d/download.partial" "$installer"; do [ ! -L "$f" ] && { [ ! -e "$f" ] || { [ -f "$f" ] && [ -O "$f" ]; }; } || { printf 'Invalid node bootstrap file.\\n' >&2; exit 1; }; done
exec 9>>"$d/download.lock"
flock -n 9 || { printf 'Another node download is running; wait and retry.\\n' >&2; exit 1; }
rm -f "$d/download.partial" || exit
trap 'rm -f "$d/download.partial"' EXIT
s=; [ "$(id -u)" -eq 0 ] || s=sudo
export http_proxy="\${http_proxy-\${HTTP_PROXY-}}" https_proxy="\${https_proxy-\${HTTPS_PROXY-}}" no_proxy="\${no_proxy-\${NO_PROXY-}}"
export HTTP_PROXY="$http_proxy" HTTPS_PROXY="$https_proxy" NO_PROXY="$no_proxy"
printf '\\n==> Downloading node installer...\\n' &&
curl -fs --connect-timeout 15 --max-time 60 --retry 2 --retry-connrefused --retry-delay 1 --max-filesize 1048576 ${quote(sourceUrl + "/api/v1/sandbox-node/install/releases/" + sourceCommit + "/node-install.pyz")} -o "$d/download.partial" || { c=$?; printf 'Cannot download node installer; check the Core URL, TLS and proxy settings.\\n' >&2; exit "$c"; }
printf '==> Verifying node installer...\\n' &&
printf '%s  %s\\n' ${quote(scriptDigest)} "$d/download.partial" | sha256sum -c --status || { printf 'Node installer checksum mismatch; generate a fresh command in Web and retry.\\n' >&2; exit 1; }
mv "$d/download.partial" "$installer" || exit
trap - EXIT
flock -u 9
exec 9>&-
`;
}

/** Runs the downloaded installer, as root in sudo mode. */
const runInstaller = `$s \${s:+--preserve-env=http_proxy,https_proxy,no_proxy,HTTP_PROXY,HTTPS_PROXY,NO_PROXY} python3 "$installer" \${NO_COLOR+--no-color}`;

/**
 * Adds this host as a node. The one-time token reaches the installer only on
 * standard input (`printf` is a shell builtin), never in an argument, the
 * environment or sudo's command line.
 */
export function nodeInstallCommand({ token, coreUrl, sourceUrl, provider, installationId, scriptDigest, sourceCommit }: {
  token: string; coreUrl: string; sourceUrl: string; provider: SandboxProvider; installationId: string; scriptDigest: string; sourceCommit: string;
}): string {
  return `${nodeInstaller(sourceUrl, scriptDigest, sourceCommit)}printf '%s\\n' ${quote(token)} | ${runInstaller} --enrollment-token-stdin --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)})`;
}

/**
 * Removes a node Core no longer lists from its host: its service, its files and,
 * when no node uses it, the service user. It holds no secret. The installer first
 * confirms with Core, at the address the node enrolled with, that the node is
 * removed; `force` skips that check, for an address that no longer answers.
 */
export function nodeUninstallCommand({ sourceUrl, installationId, scriptDigest, sourceCommit, force = false }: { sourceUrl: string; installationId: string; scriptDigest: string; sourceCommit: string; force?: boolean }): string {
  return `${nodeInstaller(sourceUrl, scriptDigest, sourceCommit)}${runInstaller} --uninstall --installation-id ${quote(installationId)}${force ? " --force" : ""})`;
}

/**
 * The node service's journal. The installer names the unit after the
 * installation (node_install.py `unit_name`), always a system unit.
 */
export function nodeLogCommand(installationId: string): string {
  const unit = `oac-node-${installationId}.service`;
  return `sudo journalctl -u ${/^[A-Za-z0-9._-]+$/.test(unit) ? unit : quote(unit)}`;
}
