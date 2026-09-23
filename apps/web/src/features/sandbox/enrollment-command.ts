const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

export function nodeInstallCommand(token: string, coreUrl: string, sourceUrl: string, provider: string, installationId: string, scriptDigest: string): string {
  return `(umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 ${quote(sourceUrl + "/node-install/node_install.py")} -o "$d/install.py" &&
printf '%s  %s\\n' ${quote(scriptDigest)} "$d/install.py" | sha256sum -c --status &&
PARSAR_NODE_ENROLLMENT_TOKEN=${quote(token)} python3 "$d/install.py" --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)})`;
}
