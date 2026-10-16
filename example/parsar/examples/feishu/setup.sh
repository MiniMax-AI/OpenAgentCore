set -eu
export LARK_LAUNCHER="$(command -v lark-cli)"
node <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
(async () => {
  const root = path.dirname(path.dirname(fs.realpathSync(process.env.LARK_LAUNCHER)));
  const version = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8')).version;
  const platform = {linux:'linux', darwin:'darwin'}[process.platform];
  const arch = {x64:'amd64', arm64:'arm64'}[process.arch];
  if (!platform || !arch) throw new Error('Unsupported lark-cli release platform');
  const asset = `lark-cli-${version}-${platform}-${arch}.tar.gz`;
  const line = fs.readFileSync(path.join(root, 'checksums.txt'), 'utf8').split('\n').find(line => line.trim().split(/\s+/)[1] === asset);
  if (!line) throw new Error('Release checksum unavailable');
  const expected = line.trim().split(/\s+/)[0];
  const urls = [
    `https://github.com/larksuite/cli/releases/download/v${version}/${asset}`,
    `https://registry.npmmirror.com/-/binary/lark-cli/v${version}/${asset}`,
  ];
  let bytes;
  for (const url of urls) {
    try {
      const response = await fetch(url, {signal: AbortSignal.timeout(60000)});
      if (!response.ok) continue;
      const candidate = Buffer.from(await response.arrayBuffer());
      if (crypto.createHash('sha256').update(candidate).digest('hex') !== expected) throw new Error('Release checksum mismatch');
      bytes = candidate;
      break;
    } catch (error) {
      if (error.message === 'Release checksum mismatch') throw error;
    }
  }
  if (!bytes) throw new Error('Could not download verified lark-cli release');
  const directory = fs.mkdtempSync(path.join(require('node:os').tmpdir(), 'lark-download-'));
  try {
    const archive = path.join(directory, asset);
    fs.writeFileSync(archive, bytes);
    execFileSync('tar', ['-xzf', archive, '-C', directory, 'lark-cli']);
    fs.mkdirSync(path.join(root, 'bin'), {recursive: true});
    fs.copyFileSync(path.join(directory, 'lark-cli'), path.join(root, 'bin/lark-cli'));
    fs.chmodSync(path.join(root, 'bin/lark-cli'), 0o755);
  } finally { fs.rmSync(directory, {recursive: true, force: true}); }
  console.log('Verified lark-cli release installed');
})().catch(error => { console.error(error.message); process.exitCode = 1; });
JS
lark-cli --version
mkdir -p outputs
printf 'lark-cli prepared before Agent startup\n' > outputs/environment-ready.txt
