import { ExecutorTurns } from "./executor_protocol.js";
import { createInterface } from "node:readline";
import { execute } from "./adapter.js";
import { parseRequest } from "./request.js";

const abort = new AbortController();
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
const stop = () => abort.abort();
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
lines.once("close", stop);
const emit = (event: {type:string;[key:string]:unknown}): Promise<void> => new Promise((resolve, reject) => {
  process.stdout.write(JSON.stringify(event) + "\n", error => error ? reject(error) : resolve());
});
try {
  const input = lines[Symbol.asyncIterator]();
  const first = await input.next();
  if (first.done || Buffer.byteLength(first.value) > 1024 * 1024) throw new Error("invalid_request");
  const request = parseRequest(first.value);
  const turns = new ExecutorTurns(emit, abort);
  const incoming = (async () => {
    try {
      for await (const line of { [Symbol.asyncIterator]: () => input }) {
        if (Buffer.byteLength(line) > 1024 * 1024) throw new Error("Invalid input.");
        const control: unknown = JSON.parse(line);
        if (!control || typeof control !== "object" || Array.isArray(control)) throw new Error("invalid_request");
        const value = control as Record<string, unknown>;
        if (value.type === "turn_start") await turns.start(value);
        else if (value.type === "turn_cancel") await turns.cancel(value);
        else await turns.submit(value);
      }
    }
    catch { abort.abort(); }
  })();
  try { await execute(request, emit, abort, turns); }
  catch { await emit({ type: "error", code: "execution_failed" }); }
  finally {
    lines.removeListener("close", stop);
    lines.close();
    process.stdin.destroy();
    await incoming;
  }
} catch {
  await emit({ type: "error", code: "invalid_request" });
} finally {
  lines.removeListener("close", stop);
  lines.close();
  process.stdin.destroy();
  process.removeListener("SIGTERM", stop);
  process.removeListener("SIGINT", stop);
}
