#!/usr/bin/env node
// Native CI assembly and installation smoke; no provider credentials or model calls.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { access, mkdir, readdir, realpath, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { buildBundle } from './build-native-installer.mjs';

const root = resolve(process.env.RUNNER_TEMP ?? '');
if (!process.env.RUNNER_TEMP) throw new Error('RUNNER_TEMP is required');
const windows = process.platform === 'win32';
const daemon = join(root, windows ? 'oac-daemon.exe' : 'oac-daemon');
// setup-node extracts the complete official Node distribution, including npm.
const node = windows ? dirname(process.execPath) : dirname(dirname(process.execPath));
async function findCodex(directory, depth = 0) {
  if (depth > 6) return [];
  try {
    await access(join(directory, 'bin', windows ? 'codex.exe' : 'codex'));
    await access(join(directory, 'codex-resources'));
    return [directory];
  } catch { /* Search the installed platform package rather than hard-code npm aliases. */ }
  const results = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) results.push(...await findCodex(join(directory, entry.name), depth + 1));
  }
  return results;
}
const candidates = await findCodex(join(root, 'native-tools', 'node_modules', '@openai'));
assert.equal(candidates.length, 1, 'Expected exactly one native Codex artifact');
const bundle = join(root, 'native-installer');
const receipt = await buildBundle({ daemon, node: await realpath(node), codex: candidates[0], claude: join(root, 'claude-runtime'), ...(!windows ? { minimax: join(root, 'minimax-runtime') } : {}), ...(process.platform === 'linux' ? { sandboxIo: join(root, 'oac-sandbox-io') } : {}), output: bundle });
console.log(JSON.stringify({ stage: 'bundle', os: receipt.os, arch: receipt.arch, components: Object.keys(receipt.components), model_requests: 0 }));
const workspace = join(root, 'native-install-workspace'); await mkdir(workspace);
const credential = join(root, 'native-install-credential.json');
const environment = randomUUID();
await writeFile(credential, JSON.stringify({ key_id: randomUUID(), executor_token: 'fixture-only-not-a-real-credential', environment_id: environment }), { mode: 0o600 });
const installDir = join(root, 'native-install-smoke');
const args = ['install', '--non-interactive', '--bundle-dir', bundle,
  '--install-dir', installDir, '--remote', 'ws://localhost:1/api/v1/agent-daemon/ws',
  '--environment-id', environment, '--workspace', workspace, '--credential-file', credential];
const command = join(bundle, windows ? 'oac-daemon.exe' : 'oac-daemon');
const env = { ...process.env, OAC_RUNTIME_HOME: join(root, 'native-install-state') };
function install(extra, expected) {
  const result = spawnSync(command, [...args, ...extra], { env, encoding: 'utf8', timeout: 120000, maxBuffer: 1024 * 1024 });
  // Keep native output out of CI logs, even for fixture-only credentials.
  assert.equal(Boolean(result.error), false, 'Installation command did not settle');
  assert.equal(result.status === 0, expected, 'Unexpected installation result');
  assert.ok(!`${result.stdout}${result.stderr}`.includes('fixture-only-not-a-real-credential'), 'Installer echoed a credential');
}
install(['--harness', 'codex'], true);
const allHarnesses = windows ? 'codex,claude' : 'codex,claude,minimax';
install(['--harness', allHarnesses], true);
install(['--harness', allHarnesses], true);
if (windows) install(['--harness', 'minimax'], false);
const installedCommand = join(installDir, 'bin', windows ? 'oac-daemon.exe' : 'oac-daemon');
const installedEnv = { ...process.env }; delete installedEnv.OAC_RUNTIME_HOME;
const installedVersion = spawnSync(installedCommand, ['version'], { env: installedEnv, encoding: 'utf8', timeout: 10000 });
assert.equal(installedVersion.status, 0, 'Installed daemon could not start');
assert.equal(installedVersion.stdout.trim(), receipt.daemon_version, 'Installed daemon identity differs');
const incomplete = spawnSync(command, ['install', '--non-interactive'], { env, input: '', encoding: 'utf8', timeout: 10000 });
assert.equal(Boolean(incomplete.error), false, 'Missing arguments waited for interaction');
assert.notEqual(incomplete.status, 0, 'Missing required arguments succeeded');
console.log(JSON.stringify({ stage: 'install', harnesses: allHarnesses.split(','), install: 'passed', additive: 'passed', reuse: 'passed', missing_arguments: 'rejected', connection: 'not_attempted', model: 'not_configured', model_requests: 0 }));
