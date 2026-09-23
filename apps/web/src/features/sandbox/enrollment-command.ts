const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

export function nodeInstallCommand(token: string, coreUrl: string, sourceUrl: string, provider: string, installationId: string, scriptDigest: string): string {
  const bootstrap = `import hashlib, urllib.request
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self, *args): raise RuntimeError("Installer redirects are not supported")
with urllib.request.build_opener(NoRedirect()).open(${JSON.stringify(sourceUrl + "/node-install/node_install.py")}, timeout=30) as response:
 code = response.read(1048577)
if len(code) > 1048576 or hashlib.sha256(code).hexdigest() != ${JSON.stringify(scriptDigest)}: raise RuntimeError("Node installer checksum mismatch")
exec(compile(code, "node_install.py", "exec"))`;
  return `PARSAR_NODE_ENROLLMENT_TOKEN=${quote(token)} python3 -c ${quote(bootstrap)} --source-url ${quote(sourceUrl)} --core-url ${quote(coreUrl)} --provider ${quote(provider)} --installation-id ${quote(installationId)}`;
}

export function enrollmentCommand(token: string, coreUrl: string): string {
  let delimiter = "PARSAR_ENROLLMENT_TOKEN";
  while (token.split("\n").includes(delimiter)) delimiter += "_END";
  return `umask 077
mkdir -p /var/lib/parsar/sandbox-node
cat > /var/lib/parsar/sandbox-node/enrollment-token <<'${delimiter}'
${token}
${delimiter}
chmod 600 /var/lib/parsar/sandbox-node/enrollment-token
parsar-sandbox-node register --config /etc/parsar/sandbox-node.json --state-dir /var/lib/parsar/sandbox-node --core-url ${quote(coreUrl)} --name node-name --max-active 4 --max-retained 16 --enrollment-token-file /var/lib/parsar/sandbox-node/enrollment-token &&
rm /var/lib/parsar/sandbox-node/enrollment-token &&
parsar-sandbox-node run --config /etc/parsar/sandbox-node.json --state-dir /var/lib/parsar/sandbox-node`;
}
