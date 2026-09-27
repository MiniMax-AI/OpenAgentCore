import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { appRoot } from './contracts.mjs'

test('route gate and Python regressions honor the selected interpreter without site packages', (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'oac-docs-python-'))
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }))
  const selected = path.join(directory, 'selected-python')
  const log = path.join(directory, 'arguments.jsonl')
  const python = spawnSync(process.env.OAC_TEST_OFFICIAL_SDK_PYTHON || 'python3', ['-S', '-c', 'import sys; print(sys.executable)'], { encoding: 'utf8' })
  assert.equal(python.status, 0, python.stderr)
  const executable = python.stdout.trim()
  // This task-owned wrapper records selection, then delegates unchanged to Python.
  fs.writeFileSync(selected, `#!${executable}\nimport json, os, sys\nwith open(${JSON.stringify(log)}, "a") as output:\n    output.write(json.dumps(sys.argv[1:]) + "\\n")\nos.execv(${JSON.stringify(executable)}, [${JSON.stringify(executable)}, *sys.argv[1:]])\n`, { mode: 0o700 })
  for (const mode of ['routes', 'tests']) {
    const result = spawnSync(process.execPath, ['scripts/check-python.mjs', mode], {
      cwd: appRoot, env: { ...process.env, OAC_TEST_OFFICIAL_SDK_PYTHON: selected }, encoding: 'utf8',
    })
    assert.equal(result.status, 0, result.stdout + result.stderr)
    if (mode === 'routes') assert.match(result.stdout, /Contract operations:\s+130/)
    else assert.match(result.stderr, /Ran 2 tests/)
  }
  const invocations = fs.readFileSync(log, 'utf8').trim().split('\n').map(line => JSON.parse(line))
  assert.equal(invocations.length, 2)
  assert.ok(invocations.every(args => args[0] === '-S'), 'Checks must not depend on ambient site packages')
})
