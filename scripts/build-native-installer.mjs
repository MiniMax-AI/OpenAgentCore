#!/usr/bin/env node
// Package trusted, already-built Linux amd64 launcher and Sandbox I/O binaries.
import { spawnSync } from 'node:child_process';
import { chmod, copyFile, lstat, mkdir, mkdtemp, rename, rm, writeFile } from 'node:fs/promises';
import { dirname, isAbsolute, join } from 'node:path';

export async function buildBundle(options) {
  if (process.platform !== 'linux' || process.arch !== 'x64') throw new Error('Native installations require Linux amd64');
  for (const required of ['daemon', 'sandboxIo', 'output']) if (!options[required]) throw new Error(`Missing --${required === 'sandboxIo' ? 'sandbox-io' : required}`);
  for (const [name, path] of Object.entries(options)) {
    if (!['daemon', 'sandboxIo', 'output'].includes(name)) throw new Error(`Unknown bundle option: ${name}`);
    if (!isAbsolute(path)) throw new Error('Bundle paths must be absolute');
  }
  try { await lstat(options.output); throw new Error('Bundle output already exists'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const result = spawnSync(options.daemon, ['version'], { encoding: 'utf8', timeout: 30000, maxBuffer: 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error('Daemon identity probe failed');
  const daemonVersion = result.stdout.trim();
  if (!/^[0-9A-Za-z][0-9A-Za-z.+_-]{0,127}$/.test(daemonVersion)) throw new Error('Invalid daemon version');
  await mkdir(dirname(options.output), { recursive: true });
  const staging = await mkdtemp(join(dirname(options.output), '.native-bundle-'));
  try {
    for (const [source, name] of [[options.daemon, 'oac-daemon'], [options.sandboxIo, 'oac-sandbox-io']]) {
      await copyFile(source, join(staging, name));
      await chmod(join(staging, name), 0o755);
    }
    const manifest = { schema: 1, daemon_version: daemonVersion, os: 'linux', arch: 'amd64' };
    await writeFile(join(staging, 'bundle.json'), JSON.stringify(manifest, null, 2) + '\n');
    await rename(staging, options.output);
    return manifest;
  } finally { await rm(staging, { recursive: true, force: true }); }
}
