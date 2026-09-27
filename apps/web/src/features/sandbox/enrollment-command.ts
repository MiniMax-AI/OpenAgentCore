const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

/**
 * How the node installer runs on the host. "sudo" (the default) runs it as root,
 * through `sudo` unless the shell already is root, which installs the node as the
 * `oac-node` system service; "user" runs it as the signed-in user, which
 * installs a user service of that user. The installer picks the mode from its
 * effective uid (deploy/install/node_install.py `main`).
 */
export type NodeInstallMode = "sudo" | "user";

/**
 * The start the node commands share: a private directory removed on exit, then
 * the console's installer, checked against its digest before anything runs. The
 * leading space keeps the command out of shell history under
 * HISTCONTROL=ignorespace. In sudo mode `s` is set before the `&&` chain, so a
 * failed download or check stops the command. It ends where the installer's own
 * line begins.
 */
function nodeInstaller(sourceUrl: string, scriptDigest: string, mode: NodeInstallMode): string {
  return ` (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT${mode === "sudo" ? `; s=; [ "$(id -u)" -eq 0 ] || s=sudo` : ""}
curl -fsS --max-time 30 --max-filesize 1048576 ${quote(sourceUrl + "/node-install/node-install.pyz")} -o "$d/node-install.pyz" &&
printf '%s  %s\\n' ${quote(scriptDigest)} "$d/node-install.pyz" | sha256sum -c --status &&
`;
}

/** Runs the downloaded installer, as root in sudo mode. */
const runInstaller = (mode: NodeInstallMode) => `${mode === "sudo" ? "$s " : ""}python3 "$d/node-install.pyz"`;

/**
 * Adds this host as a node. The one-time token reaches the installer only on
 * standard input (`printf` is a shell builtin), never in an argument, the
 * environment or sudo's command line.
 */
export function nodeInstallCommand({ token, coreUrl, sourceUrl, provider, installationId, scriptDigest, mode }: {
  token: string; coreUrl: string; sourceUrl: string; provider: "docker" | "microsandbox"; installationId: string; scriptDigest: string; mode: NodeInstallMode;
}): string {
  return `${nodeInstaller(sourceUrl, scriptDigest, mode)}printf '%s\\n' ${quote(token)} | ${runInstaller(mode)} --enrollment-token-stdin --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)})`;
}

/**
 * Removes a node Core no longer lists from its host: its service, its files and,
 * when no node uses it, the service user. It holds no secret. The installer first
 * confirms with Core, at the address the node enrolled with, that the node is
 * removed; `force` skips that check, for an address that no longer answers.
 */
export function nodeUninstallCommand({ sourceUrl, installationId, scriptDigest, mode, force = false }: { sourceUrl: string; installationId: string; scriptDigest: string; mode: NodeInstallMode; force?: boolean }): string {
  return `${nodeInstaller(sourceUrl, scriptDigest, mode)}${runInstaller(mode)} --uninstall --installation-id ${quote(installationId)}${force ? " --force" : ""})`;
}

/**
 * Installs a self-hosted executor for one Environment from this Core's
 * public address. It carries no secret: the installer asks for the executor
 * credential at a hidden prompt (or reads `--credential-file`), and rerunning
 * it resumes the same installation.
 */
export function selfHostedInstallCommand({ publicUrl, digest, environmentId, remoteUrl }: { publicUrl: string; digest: string; environmentId: string; remoteUrl: string }): string {
  return `(umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 ${quote(publicUrl + "/node-install/self-hosted-install.pyz")} -o "$d/install.pyz" &&
printf '%s  %s\\n' ${quote(digest)} "$d/install.pyz" | sha256sum -c --status &&
python3 "$d/install.pyz" --source-url ${quote(publicUrl)} --environment-id ${quote(environmentId)} --remote ${quote(remoteUrl)})`;
}

/**
 * The node service's journal. The installer names the unit after the
 * installation (node_install.py `unit_name`): a system unit in sudo mode, a user
 * unit of the node's user otherwise.
 */
export function nodeLogCommand(installationId: string, mode: NodeInstallMode): string {
  const unit = `oac-node-${installationId}.service`;
  return `${mode === "sudo" ? "sudo journalctl" : "journalctl --user"} -u ${/^[A-Za-z0-9._-]+$/.test(unit) ? unit : quote(unit)}`;
}
