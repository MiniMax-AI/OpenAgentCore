#!/usr/bin/env node
// Assemble already-qualified native artifacts; never select a latest release.
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { copyFile, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const [input, output] = process.argv.slice(2);
if (!input || !output) throw new Error('Usage: build-native-catalog.mjs INPUT OUTPUT');
const version = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
const protocol_version = (await readFile('internal/agentdaemon/proto/version.go', 'utf8')).match(/const Version = "([^"]+)"/)?.[1];
if (!protocol_version) throw new Error('Runtime protocol version is missing');
await mkdir(output, { recursive: true });
// INPUT holds <os>-<arch>.tar.gz (the daemon and bundle.json) and <os>-<arch>-<component>.tar.gz per platform.
const names = (await readdir(input)).filter(file => file.endsWith('.tar.gz')).map(file => file.slice(0, -'.tar.gz'.length)).sort();
const artifacts = {};
for (const platform of ['linux-amd64', 'darwin-arm64', 'windows-amd64']) {
  const bundle = JSON.parse(execFileSync('tar', ['-xOzf', resolve(input, `${platform}.tar.gz`), 'bundle.json'], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 }));
  if (bundle.daemon_version !== version || `${bundle.os}-${bundle.arch}` !== platform) throw new Error(`Mismatched native artifact: ${platform}`);
  const parts = Object.keys(bundle.components).map(component => `${platform}-${component}`).sort();
  if (JSON.stringify(names.filter(name => name.startsWith(platform + '-'))) !== JSON.stringify(parts)) throw new Error(`Native archives do not match the components of ${platform}`);
  for (const name of [platform, ...parts]) {
    const archive = resolve(input, `${name}.tar.gz`);
    const hash = createHash('sha256');
    for await (const chunk of createReadStream(archive)) hash.update(chunk);
    artifacts[name] = { sha256: hash.digest('hex') };
    await copyFile(archive, join(output, `${name}.tar.gz`));
  }
}
await writeFile(join(output, 'catalog.json'), JSON.stringify({ version, protocol_version, artifacts }, null, 2)+'\n');
