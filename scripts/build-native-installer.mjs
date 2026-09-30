#!/usr/bin/env node
// Package trusted, already-built native artifacts. This does not install on a host.
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmod, copyFile, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, stat, writeFile } from 'node:fs/promises';
import { basename, dirname, isAbsolute, join, relative, sep } from 'node:path';
import { createRequire } from 'node:module';

export const pins = { node: '22.22.0', codex: '0.153.4', claude: '0.3.269', minimax: '0.4.12' };
const platforms = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
const architectures = { x64: 'amd64', arm64: 'arm64' };
const inside = (root, path) => {
  const rel = relative(root, path);
  return rel !== '..' && !rel.startsWith('..' + sep) && !isAbsolute(rel);
};

// Resolve existing parents even when the output does not exist yet. macOS
// temporary paths and Windows short paths can alias a component source.
async function outputRealPath(path) {
  try { return await realpath(path); }
  catch (error) {
    if (error.code !== 'ENOENT' || dirname(path) === path) throw error;
    return join(await outputRealPath(dirname(path)), basename(path));
  }
}

// Flatten contained links, including directory links used by pnpm exports. Every
// traversal checks its resolved target; active ancestors detect directory cycles.
export async function copyComponent(source, destination) {
  const root = await realpath(source);
  if (!(await stat(root)).isDirectory()) throw new Error('Component source must be a directory');
  const files = {};
  async function visit(path, target, ancestors) {
    const resolved = await realpath(path);
    if (!inside(root, resolved)) throw new Error('Component link escapes its source');
    const info = await stat(resolved);
    if (info.isDirectory()) {
      if (ancestors.has(resolved)) throw new Error('Component contains a directory link cycle');
      const next = new Set([...ancestors, resolved]);
      await mkdir(target);
      for (const name of (await readdir(resolved)).sort()) {
        // Manifest paths are portable slash paths, never Windows aliases/streams.
        if (/[\\:\x00-\x1f]/.test(name) || /[. ]$/.test(name) || /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(name)) {
          throw new Error('Component contains a non-portable file name');
        }
        await visit(join(resolved, name), join(target, name), next);
      }
      return;
    }
    if (!info.isFile()) throw new Error('Component contains a non-regular file');
    await copyFile(resolved, target);
    const executable = Boolean(info.mode & 0o111) || /\.(exe|cmd|bat)$/i.test(target);
    await chmod(target, executable ? 0o755 : 0o644);
    files[relative(destination, target).split(sep).join('/')] = {
      sha256: createHash('sha256').update(await readFile(target)).digest('hex'), executable,
    };
  }
  await visit(root, destination, new Set());
  if (!Object.keys(files).length) throw new Error('Component is empty');
  return files;
}

function probe(command, args, cwd, env) {
  const result = spawnSync(command, args, { cwd, env, input: '', encoding: 'utf8', timeout: 30000, killSignal: 'SIGKILL', maxBuffer: 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error('Native component compatibility probe failed');
  return result.stdout.trim();
}
const json = async path => JSON.parse(await readFile(path, 'utf8'));
const requireFile = async path => { if (!(await stat(path)).isFile()) throw new Error('Required component file is missing'); };

// Flattening Node's Unix bin links moves npm's relative requires. Keep the
// original package entrypoints and publish relocatable regular-file launchers.
export async function prepareNodeEntrypoints(root, files) {
  if (process.platform === 'win32') return;
  for (const name of ['npm', 'npx']) {
    const entry = `lib/node_modules/npm/bin/${name}-cli.js`;
    await requireFile(join(root, entry));
    const launcher = '#!/bin/sh\n' +
      'basedir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1\n' +
      `exec "$basedir/node" "$basedir/../${entry}" "$@"\n`;
    const path = `bin/${name}`;
    await writeFile(join(root, path), launcher, { mode: 0o755 });
    await chmod(join(root, path), 0o755);
    files[path] = { sha256: createHash('sha256').update(launcher).digest('hex'), executable: true };
  }
}

export async function validateNodeCommands(nodeRoot, cwd) {
  const windows = process.platform === 'win32';
  const bin = windows ? nodeRoot : join(nodeRoot, 'bin');
  const npmRoot = join(nodeRoot, windows ? 'node_modules/npm' : 'lib/node_modules/npm');
  const version = (await json(join(npmRoot, 'package.json'))).version;
  const env = { ...process.env, PATH: [bin, process.env.PATH ?? ''].join(windows ? ';' : ':') };
  for (const name of ['npm', 'npx']) {
    await requireFile(join(bin, windows ? `${name}.cmd` : name));
    // Windows command scripts require cmd.exe; only these fixed command names
    // enter the shell. Unix probes use executable lookup from the installed PATH.
    const observed = windows
      ? probe(process.env.ComSpec ?? 'cmd.exe', ['/d', '/s', '/c', `${name} --version`], cwd, env)
      : probe(name, ['--version'], cwd, env);
    if (observed !== version) throw new Error('Bundled npm entrypoint compatibility failed');
  }
}

export async function validateComponents(root, names, daemonVersion) {
  const windows = process.platform === 'win32';
  const nodeRoot = join(root, 'components', 'node');
  const node = join(nodeRoot, windows ? 'node.exe' : 'bin/node');
  const env = { ...process.env, PATH: [dirname(node), join(root, 'components', 'minimax', 'bin'), process.env.PATH ?? ''].join(sep === '\\' ? ';' : ':') };
  if (probe(node, ['--version'], root, env) !== `v${pins.node}`) throw new Error('Node version does not match the pin');
  const identity = JSON.parse(probe(node, ['-p', 'JSON.stringify([process.platform,process.arch])'], root, env));
  if (identity[0] !== process.platform || identity[1] !== process.arch) throw new Error('Node platform does not match the build host');
  await validateNodeCommands(nodeRoot, root);
  const daemon = join(root, windows ? 'oac-daemon.exe' : 'oac-daemon');
  if (probe(daemon, ['version'], root, env) !== daemonVersion) throw new Error('Daemon identity changed during packaging');
  for (const name of names) {
    const component = join(root, 'components', name);
    if (name === 'codex') {
      if (!(await stat(join(component, 'codex-resources'))).isDirectory()) throw new Error('Codex resources are missing');
      const binary = join(component, 'bin', windows ? 'codex.exe' : 'codex');
      if (probe(binary, ['--version'], component, env) !== `codex-cli ${pins.codex}`) throw new Error('Codex version does not match the pin');
    } else if (name === 'claude') {
      const manifest = await json(join(component, 'package.json'));
      if (manifest.dependencies?.['@anthropic-ai/claude-agent-sdk']?.split('(')[0] !== pins.claude) throw new Error('Claude SDK pin mismatch');
      await requireFile(join(component, 'pnpm-lock.yaml'));
      await requireFile(join(component, 'dist/main.js'));
      const info = JSON.parse(probe(node, [join(component, 'dist/runtime_check.js')], component, env));
      if (info.type !== 'runtime_ready' || info.protocol !== 3 || info.sdk !== pins.claude || info.native !== '2.1.269 (Claude Code)' || !info.features?.includes('local_runtime_v2')) throw new Error('Claude runtime compatibility failed');
    } else if (name === 'minimax') {
      if (windows) throw new Error('MiniMax is not supported on Windows');
      const upstream = await json(join(component, 'source.json'));
      const expected = await json(new URL('../packages/mcode-harness/source.json', import.meta.url));
      if (upstream.version !== pins.minimax || upstream.revision !== expected.revision) throw new Error('MiniMax source pin mismatch');
      for (const entry of ['launch.mjs', 'dist/worker.mjs', 'provenance.json', 'native-patch.json']) await requireFile(join(component, entry));
      if (probe(node, [join(component, 'native/cli.js'), '--version'], component, env) !== pins.minimax) throw new Error('MiniMax native pin mismatch');
      const info = JSON.parse(probe(node, [join(component, 'check.mjs')], component, env));
      if (info.protocol !== 2 || info.native !== pins.minimax || info.source !== expected.revision) throw new Error('MiniMax companion compatibility failed');
    }
  }
}

export async function buildBundle(options) {
  if (!platforms[process.platform] || !architectures[process.arch]) throw new Error('Unsupported native bundle platform');
  for (const required of ['daemon', 'node', 'output']) if (!options[required]) throw new Error(`Missing --${required}`);
  for (const path of Object.values(options)) if (!isAbsolute(path)) throw new Error('Bundle paths must be absolute');
  const names = ['codex', 'claude', 'minimax'].filter(name => options[name]);
  if (!names.length) throw new Error('At least one Harness source is required');
  if (process.platform === 'win32' && options.minimax) throw new Error('MiniMax is not supported on Windows');
  try { await lstat(options.output); throw new Error('Bundle output already exists'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const output = await outputRealPath(options.output);
  // Refuse staging inside a source: recursive copying must not ingest its own output.
  for (const name of ['node', ...names]) if (inside(await realpath(options[name]), output)) throw new Error('Output must be outside component sources');
  const daemonVersion = probe(options.daemon, ['version'], dirname(options.daemon), process.env);
  if (!/^[0-9A-Za-z][0-9A-Za-z.+_-]{0,127}$/.test(daemonVersion)) throw new Error('Invalid daemon version');
  await mkdir(dirname(output), { recursive: true });
  const staging = await mkdtemp(join(dirname(output), '.native-bundle-'));
  try {
    const daemon = join(staging, process.platform === 'win32' ? 'oac-daemon.exe' : 'oac-daemon');
    await copyFile(options.daemon, daemon); await chmod(daemon, 0o755);
    await mkdir(join(staging, 'components'));
    const components = {};
    for (const name of ['node', ...names]) components[name] = { version: pins[name], files: await copyComponent(options[name], join(staging, 'components', name)) };
    await prepareNodeEntrypoints(join(staging, 'components', 'node'), components.node.files);
    if (components.minimax) {
      const component = join(staging, 'components', 'minimax');
      const nativeRequire = createRequire(join(component, 'native', 'cli.js'));
      const rg = await realpath(nativeRequire('@vscode/ripgrep').rgPath);
      if (!inside(component, rg)) throw new Error('MiniMax ripgrep escaped the component');
      await requireFile(rg);
      await mkdir(join(component, 'bin'), { recursive: true });
      await copyFile(rg, join(component, 'bin', 'rg')); await chmod(join(component, 'bin', 'rg'), 0o755);
      components.minimax.files['bin/rg'] = { sha256: createHash('sha256').update(await readFile(rg)).digest('hex'), executable: true };
    }
    await validateComponents(staging, names, daemonVersion);
    const manifest = { schema: 1, daemon_version: daemonVersion, os: platforms[process.platform], arch: architectures[process.arch], components };
    await writeFile(join(staging, 'bundle.json'), JSON.stringify(manifest, null, 2) + '\n');
    await rename(staging, output);
    return manifest;
  } finally { await rm(staging, { recursive: true, force: true }); }
}
