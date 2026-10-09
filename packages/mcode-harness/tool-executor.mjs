import { spawn } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { isAbsolute, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const limit = 16 * 1024 * 1024;
const worker = fileURLToPath(new URL('./dist/worker.mjs', import.meta.url));

// One bridge owns every worker until its stdio and native tools have settled.
export class ToolExecutor {
  #calls = new Set();
  #closed = false;

  constructor({ workspace, scratch }, entrypoint = worker) {
    if (!isAbsolute(workspace) || normalize(workspace) !== workspace)
      throw new Error('Invalid workspace profile');
    mkdirSync(scratch, { recursive: true });
    this.workspace = workspace;
    this.entrypoint = entrypoint;
  }

  async execute(tool, input, signal) {
    if (this.#closed) throw new Error('Workspace transport is closed');
    signal?.throwIfAborted();
    const request = JSON.stringify({ tool, input });
    if (Buffer.byteLength(request) > limit) throw new Error('Workspace tool input exceeds limit');
    const child = spawn(process.execPath, [this.entrypoint, this.workspace], {
      cwd: this.workspace,
      env: process.env,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const call = { stop: () => child.kill('SIGTERM') };
    this.#calls.add(call);
    let output = Buffer.alloc(0);
    let failure;
    signal?.addEventListener('abort', call.stop, { once: true });
    if (signal?.aborted) call.stop();
    child.on('error', error => { failure = error; });
    child.stdin.on('error', error => { failure = error; call.stop(); });
    child.stderr.resume();
    child.stdout.on('data', chunk => {
      if (output.length + chunk.length > limit) {
        failure = new Error('Workspace tool output exceeds limit');
        call.stop();
      } else output = Buffer.concat([output, chunk]);
    });
    call.settled = new Promise(resolve => child.on('close', resolve));
    child.stdin.end(request);
    try {
      const code = await call.settled;
      if (failure) throw failure;
      signal?.throwIfAborted();
      if (this.#closed) throw new Error('Workspace transport is closed');
      const result = JSON.parse(output.toString('utf8'));
      if (result.tool_name !== tool || typeof result.text !== 'string' || !Array.isArray(result.content))
        throw new Error('Invalid native workspace tool result');
      if (code !== 0 && result.isError !== true) throw new Error('Workspace tool execution failed');
      return result;
    } finally {
      signal?.removeEventListener('abort', call.stop);
      this.#calls.delete(call);
    }
  }

  async close() {
    this.#closed = true;
    const calls = [...this.#calls];
    for (const call of calls) call.stop();
    await Promise.all(calls.map(call => call.settled));
  }
}
