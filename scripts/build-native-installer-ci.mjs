#!/usr/bin/env node
// Native CI assembly and installation smoke; no provider credentials or model calls.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { buildBundle } from './build-native-installer.mjs';

if (!process.env.RUNNER_TEMP) throw new Error('RUNNER_TEMP is required');
const root = resolve(process.env.RUNNER_TEMP);
const bundle = join(root, 'native-installer');
const receipt = await buildBundle({ daemon: join(root, 'oac-daemon'), sandboxIo: join(root, 'oac-sandbox-io'), output: bundle });
console.log(JSON.stringify({ stage: 'bundle', os: receipt.os, arch: receipt.arch, programs: ['oac-daemon', 'oac-sandbox-io'], model_requests: 0 }));
const workspace = join(root, 'native-install-workspace'); await mkdir(workspace);
const credential = join(root, 'native-install-credential.json');
const environment = randomUUID();
await writeFile(credential, JSON.stringify({ key_id: randomUUID(), executor_token: 'fixture-only-not-a-real-credential', environment_id: environment }), { mode: 0o600 });
const installDir = join(root, 'native-install-smoke');
const args = ['install', '--non-interactive', '--bundle-dir', bundle,
  '--install-dir', installDir, '--remote', 'ws://localhost:1/api/v1/agent-daemon/ws',
  '--environment-id', environment, '--workspace', workspace, '--credential-file', credential];
const command = join(bundle, 'oac-daemon');
const env = { ...process.env, OAC_RUNTIME_HOME: join(root, 'native-install-state') };
for (let attempt = 0; attempt < 2; attempt++) {
  const result = spawnSync(command, args, { env, encoding: 'utf8', timeout: 120000, maxBuffer: 1024 * 1024 });
  assert.equal(Boolean(result.error), false, 'Installation command did not settle');
  assert.equal(result.status, 0, 'Installation or reuse failed');
  assert.ok(!`${result.stdout}${result.stderr}`.includes('fixture-only-not-a-real-credential'), 'Installer echoed a credential');
}
const installedEnv = { ...process.env }; delete installedEnv.OAC_RUNTIME_HOME;
const installedVersion = spawnSync(join(installDir, 'bin', 'oac-daemon'), ['version'], { env: installedEnv, encoding: 'utf8', timeout: 10000 });
assert.equal(installedVersion.status, 0, 'Installed daemon could not start');
assert.equal(installedVersion.stdout.trim(), receipt.daemon_version, 'Installed daemon identity differs');
const incomplete = spawnSync(command, ['install', '--non-interactive'], { env, input: '', encoding: 'utf8', timeout: 10000 });
assert.equal(Boolean(incomplete.error), false, 'Missing arguments waited for interaction');
assert.notEqual(incomplete.status, 0, 'Missing required arguments succeeded');
console.log(JSON.stringify({ stage: 'install', install: 'passed', reuse: 'passed', missing_arguments: 'rejected', connection: 'not_attempted', model_requests: 0 }));
