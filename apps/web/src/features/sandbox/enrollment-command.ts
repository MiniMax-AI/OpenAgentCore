export function enrollmentCommand(token: string, coreUrl: string): string {
  let delimiter = "PARSAR_ENROLLMENT_TOKEN";
  while (token.split("\n").includes(delimiter)) delimiter += "_END";
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
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
