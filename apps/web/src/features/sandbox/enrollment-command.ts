const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

export function nodeInstallCommand(token: string, coreUrl: string, sourceUrl: string, provider: string, installationId: string, scriptDigest: string): string {
  return `(umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 ${quote(sourceUrl + "/node-install/node-install.pyz")} -o "$d/install.py" &&
printf '%s  %s\\n' ${quote(scriptDigest)} "$d/install.py" | sha256sum -c --status &&
PARSAR_NODE_ENROLLMENT_TOKEN=${quote(token)} python3 "$d/install.py" --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)})`;
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

/** The node service's journal: the installer names its systemd user unit after the installation (deploy/install/node_install.py:340, 410-411). */
export function nodeLogCommand(installationId: string): string {
  const unit = `parsar-node-${installationId}.service`;
  return `journalctl --user -u ${/^[A-Za-z0-9._-]+$/.test(unit) ? unit : quote(unit)}`;
}
