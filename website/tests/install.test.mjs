import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const script = fileURLToPath(new URL('../install.sh', import.meta.url))
const release = 'https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh'

test('domain installer forwards arguments and exit status, never executes a failed download', () => {
  const root = mkdtempSync(join(tmpdir(), 'oac-domain-installer-'))
  try {
    const temporary = join(root, 'temporary')
    mkdirSync(temporary)
    writeFileSync(join(root, 'curl'), `#!/usr/bin/env bash
set -eu
printf '%s\\n' "$@" > "$CURL_ARGS"
while [[ $# -gt 0 ]]; do
  if [[ "$1" == --output ]]; then target="$2"; break; fi
  shift
done
printf '%s\\n' '#!/usr/bin/env bash' 'printf "%s\\n" "$@"' 'exit 17' > "$target"
exit "$DOWNLOAD_STATUS"
`)
    chmodSync(join(root, 'curl'), 0o700)
    const args = ['--upgrade', '--install-dir', '/path with spaces/core', '--version', 'v0.0.10']
    for (const downloadStatus of [0, 22]) {
      const result = spawnSync('bash', ['-s', '--', ...args], {
        input: readFileSync(script), encoding: 'utf8',
        env: { ...process.env, PATH: `${root}:${process.env.PATH}`, TMPDIR: temporary, CURL_ARGS: join(root, 'curl-args'), DOWNLOAD_STATUS: String(downloadStatus) },
      })
      assert.equal(result.status, downloadStatus || 17, result.stderr)
      assert.equal(result.stdout, downloadStatus ? '' : args.join('\n') + '\n')
      const curlArgs = readFileSync(join(root, 'curl-args'), 'utf8').trim().split('\n')
      assert.ok(curlArgs.includes(release))
      assert.equal(curlArgs[curlArgs.indexOf('--proto') + 1], '=https')
      assert.equal(curlArgs[curlArgs.indexOf('--proto-redir') + 1], '=https')
      assert.deepEqual(readdirSync(temporary), [])
    }
  } finally { rmSync(root, { recursive: true, force: true }) }
})
