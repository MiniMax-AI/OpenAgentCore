#!/usr/bin/env node
// Decode YAML with the locked Node dependency; Python checks need only stdlib.
import fs from 'node:fs'
import { spawnSync } from 'node:child_process'
import yaml from 'js-yaml'
import { appRoot, surfaces, sourcePath } from './contracts.mjs'

const mode = process.argv[2]
if (!['routes', 'tests'].includes(mode)) throw new Error('Expected routes or tests')
const args = mode === 'routes'
  ? ['scripts/verify-contract-routes.py']
  : ['-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_*.py']
const input = mode === 'routes' ? JSON.stringify(surfaces.map(surface => ({
  label: surface.id, document: yaml.load(fs.readFileSync(sourcePath(surface), 'utf8')),
}))) : undefined
const result = spawnSync(process.env.OAC_TEST_OFFICIAL_SDK_PYTHON || 'python3', ['-S', ...args], {
  cwd: appRoot, input, stdio: ['pipe', 'inherit', 'inherit'],
})
if (result.error) throw result.error
process.exit(result.status ?? 1)
