#!/usr/bin/env node
// Assemble already-qualified native artifacts; never select a latest release.
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const [input, output] = process.argv.slice(2);
if (!input || !output) throw new Error('Usage: build-native-catalog.mjs INPUT OUTPUT');
const version = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
const protocol_version = (await readFile('internal/agentdaemon/proto/version.go', 'utf8')).match(/const Version = "([^"]+)"/)?.[1];
if (!protocol_version) throw new Error('Runtime protocol version is missing');
await mkdir(output, { recursive: true });
const artifacts = {};
for (const [ci, platform] of Object.entries({ 'Linux-X64': 'linux-amd64', 'macOS-ARM64': 'darwin-arm64', 'Windows-X64': 'windows-amd64' })) {
  const archive = resolve(input, `oac-native-installer-${ci}.tar.gz`);
  const bundle = JSON.parse(execFileSync('tar', ['-xOzf', archive, './bundle.json'], { encoding: 'utf8' }));
  if (bundle.daemon_version !== version || `${bundle.os}-${bundle.arch}` !== platform) throw new Error(`Mismatched native artifact: ${platform}`);
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(archive)) hash.update(chunk);
  artifacts[platform] = { sha256: hash.digest('hex') };
  await copyFile(archive, join(output, `${platform}.tar.gz`));
}
await writeFile(join(output, 'catalog.json'), JSON.stringify({ version, protocol_version, artifacts }, null, 2)+'\n');
