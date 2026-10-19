import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, symlink, rm, rename, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { WorkspaceDirectories } from "../dist/workspace_directories.js";

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "oac-directories-"));
  const workspace = join(root, "workspace");
  await mkdir(workspace);
  const abort = new AbortController();
  let receipt;
  const directories = new WorkspaceDirectories(event => { receipt?.(event); return Promise.resolve(); }, abort);
  await directories.bind(workspace);
  t.after(async () => { await directories.close(); await rm(root, { recursive: true, force: true }); });
  let sequence = 0;
  return { root, workspace, directories, abort, list: async (directory = "", max_entries = 1000) => {
    const result = new Promise(resolve => { receipt = resolve; });
    directories.submit({ type: "workspace_directory", id: `list-${++sequence}`, directory, max_entries });
    const value = await result;
    await new Promise(resolve => setImmediate(resolve));
    return value;
  } };
}

test("bounded literal metadata, missing directory, and traversal denial", async t => {
  const f = await fixture(t);
  await mkdir(join(f.workspace, "nested"));
  await writeFile(join(f.workspace, "binary"), Buffer.from([0, 255, 0]));
  await writeFile(join(f.workspace, "empty"), "");
  await writeFile(join(f.root, "protected"), "secret");
  await symlink(f.root, join(f.workspace, "outside"));
  await symlink("nested", join(f.workspace, "inside"));
  const value = await f.list();
  assert.equal(value.truncated, false);
  assert.deepEqual(value.entries.sort((a, b) => a.name.localeCompare(b.name)), [
    { name: "binary", kind: "file", size_bytes: 3 }, { name: "empty", kind: "file", size_bytes: 0 },
    { name: "inside", kind: "symlink" }, { name: "nested", kind: "directory" }, { name: "outside", kind: "symlink" },
  ]);
  assert.deepEqual((await f.list("nested")).entries, []);
  assert.equal((await f.list("", 1)).truncated, true);
  assert.equal((await f.list("absent")).error, "not_found");
  for (const path of ["inside", "outside", "outside/workspace", "binary"]) assert.equal((await f.list(path)).error, "invalid");
  for (const path of ["/", "a//b", ".", "..", "a/../b", "a\\b", "a\n"]) assert.equal((await f.list(path)).error, "invalid");
  for (const limit of [0, -1, 1001, 1.1]) assert.equal((await f.list("", limit)).error, "invalid");
});

test("literal Unicode names are preserved and undecodable names fail explicitly", {skip:process.platform === "win32"}, async t => {
  const f = await fixture(t);
  await writeFile(join(f.workspace, "字\ufffd"), "ok");
  assert.deepEqual((await f.list()).entries, [{ name: "字\ufffd", kind: "file", size_bytes: 2 }]);
  await writeFile(Buffer.concat([Buffer.from(f.workspace + "/"), Buffer.from([255])]), "invalid");
  assert.equal((await f.list()).error, "uncertain");
  assert.equal(f.abort.signal.aborted, true);
});
