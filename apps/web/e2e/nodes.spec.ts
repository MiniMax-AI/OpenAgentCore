import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole, setNode, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("adds a node: host requirements, a countdown, the same command after closing, a new one after expiry, then its own node's registration", async ({ page, request }) => {
  await page.clock.install();
  // Core counts a token's ten minutes on its own clock; the page's clock stands in for it, so fast-forwarding expires a command.
  await page.route("**/core/v1/sandbox/enrollment-tokens", async (route) => {
    const response = await route.fetch();
    const now = await page.evaluate(() => Date.now());
    await route.fulfill({ response, json: { ...await response.json(), expires_at: new Date(now + 10 * 60_000).toISOString() } });
  });
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Add node" }).click();
  const add = page.getByRole("dialog", { name: "Add node" });
  // What a Docker host needs, with the root commands that prepare it.
  await expect(add.getByText("Docker at /var/run/docker.sock for that user, enforcing CPU and memory limits")).toBeVisible();
  await expect(add.getByText("sudo usermod -aG docker NODE_USER")).toBeVisible();
  await expect(add.getByText("CPUs and memory for at least one sandbox: 2 CPU · 4 GiB")).toBeVisible();
  await expect(add.getByText(/^Can reach http:\/\/127\.0\.0\.1:\d+ and https:\/\/core\.example\.com; sandboxes must reach https:\/\/core\.example\.com$/)).toBeVisible();
  await expect(add.getByText(/\/dev\/kvm/)).toHaveCount(0);
  // The fixture console runs on loopback, where another machine can't download from it.
  await expect(add.getByRole("note")).toContainText("other machines can't reach");
  await add.getByLabel("Sandboxes at once").fill("3");
  const tokenRequest = () => page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith("/core/v1/sandbox/enrollment-tokens"));
  const issued = tokenRequest();
  await add.getByRole("button", { name: "Generate command" }).click();
  // Docker never suspends, so it retains exactly the sandboxes it runs.
  expect((await issued).postDataJSON()).toEqual({ max_active: 3, max_retained: 3 });
  const field = add.getByLabel("One-time enrollment command");
  await expect(field).toHaveValue(/enroll_fixture_/);
  await expect(add.getByRole("timer")).toHaveText(/^Expires in (10:00|9:\d\d)$/);
  const progress = add.getByRole("status", { name: "Registration progress" });
  await expect(progress).toHaveText(/Waiting for registration.*Connect.*Docker check/);

  // Closing keeps the command for the next opening. Meanwhile another command's node, with the
  // same limits, registers: it reports another enrollment ID, so it is not this command's node.
  const first = await field.inputValue();
  await add.getByRole("button", { name: "Close dialog" }).click();
  await expect(add).toBeHidden();
  await setNode(request, { id: "node-other", name: "edge-05", enrollment_id: "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a" });
  await page.getByRole("button", { name: "Add node" }).click();
  await expect(field).toHaveValue(first);
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).toContainText("edge-05");
  await expect(progress).toHaveText(/Waiting for registration.*Connect.*Docker check/);

  // Once expired, a new command is issued only on request, for the same limits.
  await page.clock.fastForward("10:30");
  await expect(progress).toContainText("Command expired");
  await expect(field).toBeHidden();
  const reissued = tokenRequest();
  await add.getByRole("button", { name: "Generate new command" }).click();
  expect((await reissued).postDataJSON()).toEqual({ max_active: 3, max_retained: 3 });
  await expect(field).toHaveValue(/enroll_fixture_/);
  await expect(field).not.toHaveValue(first);

  // The node registers while the dialog is closed and the command lapses: reopening reads the
  // node list, and the node reporting the command's enrollment ID outranks its expiry.
  await add.getByRole("button", { name: "Close dialog" }).click();
  await expect(add).toBeHidden();
  await setNode(request, { id: "node-new", name: "edge-04" });
  await page.clock.fastForward("10:30");
  // Every render from the reopening on is watched, so even a brief "Command expired" would count.
  await page.evaluate(() => {
    const watch = window as unknown as { expiredShown: boolean; expiredWatch: MutationObserver };
    watch.expiredShown = false;
    watch.expiredWatch = new MutationObserver(() => { if (document.body.textContent?.includes("Command expired")) watch.expiredShown = true; });
    watch.expiredWatch.observe(document.body, { childList: true, subtree: true, characterData: true });
  });
  await page.getByRole("button", { name: "Add node" }).click();
  await expect(progress).toHaveText(/Registered · edge-04.*Waiting to connect.*Docker check/);
  expect(await page.evaluate(() => {
    const watch = window as unknown as { expiredShown: boolean; expiredWatch: MutationObserver };
    watch.expiredWatch.disconnect();
    return watch.expiredShown;
  })).toBe(false);
  await expect(add.getByText("Rerun only on edge-04 if asked")).toBeVisible();
  // Past the installer's minute without connecting, the dialog points at the node's log.
  await page.clock.fastForward("01:01");
  const problem = add.getByRole("alert");
  await expect(problem).toContainText("Not connected yet");
  await expect(problem).toContainText("journalctl --user -u parsar-node-7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f.service");
  // Connected, it reports why Docker isn't ready; once ready, the node is connected.
  await setNode(request, { id: "node-new", online: true, diagnostic: "docker_limits_unsupported" });
  await expect(problem).toContainText("Docker limits unsupported");
  await expect(progress).toContainText("Waiting for Docker");
  await setNode(request, { id: "node-new", provider_ready: true, diagnostic: "" });
  await expect(progress).toHaveText("edge-04 · Connected");
  await add.getByRole("button", { name: "Done" }).click();
  await expect(add).toBeHidden();
  // A finished flow leaves no limits behind: the next node starts from the defaults.
  await page.getByRole("button", { name: "Add node" }).click();
  await expect(add.getByLabel("Sandboxes at once")).toHaveValue("2");
});

test("says the console has no node files for the provider and issues no command", async ({ page, request }) => {
  // A thin bundle: the console holds no node files at all.
  await openConsole(page, request, "nodes", { nodeArtifacts: [] });
  await page.getByRole("button", { name: "Add node" }).click();
  const add = page.getByRole("dialog", { name: "Add node" });
  await expect(add.getByRole("status")).toHaveText("This console has no node files for Docker. Install Core from the offline bundle, or add the release artifacts and rerun ./install.sh.");
  await expect(add.getByRole("button", { name: "Generate command" })).toHaveCount(0);
  expect(await writes(request)).toEqual([]);
});

test("removes a node after confirmation", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Remove edge-03" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove node" });
  await confirm.getByRole("button", { name: "Confirm removal" }).click();
  await expect(confirm).toBeHidden();
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).not.toContainText("edge-03");
});

test("sets up own-machine sandboxes page by page, with the Runtime from the distribution", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await expect(page.getByRole("heading", { name: "Where should sandboxes run?" })).toBeVisible();
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "Docker" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  await expect(page.getByRole("heading", { name: "Review and save" })).toBeVisible();
  await page.getByRole("button", { name: "Save configuration" }).click();

  // The saved specification carries the Runtime read from the console's manifest.
  await expect(page.getByText("c0ffee000000")).toBeVisible();
  // Own machines continue straight to adding the first node, at its limits: no command is issued yet.
  await expect(page.getByRole("dialog", { name: "Add node" }).getByLabel("Sandboxes at once")).toBeVisible();
  // None is requested within a second of opening, and Core saw only the deployment write.
  const tokenRequested = await page.waitForRequest((sent) => sent.url().endsWith("/core/v1/sandbox/enrollment-tokens"), { timeout: 1000 }).then(() => true, () => false);
  expect(tokenRequested).toBe(false);
  expect(await writes(request)).toEqual(["POST /core/v1/sandbox/deployment"]);
});

test("saves E2B without opening Add node, as it has no machines", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud" }).click();
  await page.getByLabel("E2B API key").fill("fixture-private-key");
  await page.getByLabel("Template build").fill("template:94be54a1-138c-4f30-bc87-b13686272dbe");
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Save configuration" }).click();
  await expect(page.getByRole("heading", { name: "Sandbox backend", level: 1 })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("keeps the saved size and Runtime for the same backend, and starts another from its defaults", async ({ page, request }) => {
  const runtime = { source_commit: "0".repeat(40), image_id: `sha256:${"a".repeat(64)}`, image_manifest_digest: `sha256:${"b".repeat(64)}`,
    microsandbox_ref: `parsar-core-runtime@sha256:${"b".repeat(64)}`, runtime_sha256: "c".repeat(64), firmware_sha256: "d".repeat(64) };
  const current = { resources: { cpus: 7, memory_mib: 8192 }, runtime };
  const deployment = { installation_id: "94be54a1-138c-4f30-bc87-b13686272dbe", provider: "docker", core_url: "https://core.example", maintenance: true,
    owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 0, pending: 0 }, specification: current };
  let submitted: Record<string, unknown> | null = null;
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    if (route.request().method() === "PUT") submitted = route.request().postDataJSON() as Record<string, unknown>;
    await route.fulfill({ json: deployment });
  });
  await page.route("**/core/v1/sandbox/nodes", (route) => route.fulfill({ json: { data: [] } }));
  await openConsole(page, request, "nodes");
  const save = page.getByRole("button", { name: "Save and stay in maintenance" });
  const back = page.getByRole("button", { name: "Back" });

  // The same backend keeps its saved size and Runtime.
  await page.getByRole("button", { name: "Change provider or resources" }).click();
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "Docker" }).click();
  await page.getByRole("button", { name: /^Current/ }).click();
  await save.click();
  await expect.poll(() => submitted?.resources).toEqual(current.resources);
  expect(submitted?.runtime).toEqual(runtime);
  // Core's address is config.json's public_url: a change never sends it.
  expect(submitted).not.toHaveProperty("core_url");

  // Another backend starts from its own size, with disks, and this console's Runtime.
  await back.click();
  await back.click();
  await page.getByRole("button", { name: "microsandbox" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  await save.click();
  await expect.poll(() => submitted?.provider).toBe("microsandbox");
  expect(submitted?.resources).toEqual({ cpus: 2, memory_mib: 4096, root_disk_mib: 8192, environment_disk_mib: 8192 });
  expect(submitted?.runtime).toMatchObject({ source_commit: "c0ffee".padEnd(40, "0") });

  // E2B needs its key again and takes its size from the template build: no size, Runtime or disks.
  await back.click();
  await back.click();
  await back.click();
  await page.getByRole("button", { name: "E2B cloud" }).click();
  await page.getByLabel("E2B API key").fill("fixture-private-key");
  await page.getByLabel("Template build").fill("template:94be54a1-138c-4f30-bc87-b13686272dbe");
  await page.getByRole("button", { name: "Next" }).click();
  await save.click();
  await expect.poll(() => submitted?.provider).toBe("e2b");
  expect(submitted?.resources).toBeUndefined();
  expect(submitted?.runtime).toBeUndefined();
});

test("reports a failed sandbox change in a dialog, then reads the state again", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.route("**/core/v1/sandbox/deployment/maintenance", (route) => route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: { message: "The deployment changed.", type: "conflict_error", code: "sandbox_deployment_conflict", param: null } }),
  }));
  await page.getByRole("button", { name: "Enter maintenance to change provider" }).click();
  const failed = page.getByRole("dialog", { name: "Couldn't confirm the sandbox change" });
  await failed.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(failed).toBeHidden();
  await expect(page.getByRole("button", { name: "Enter maintenance to change provider" })).toBeEnabled();
});

test("renames a node and sets how many sandboxes run on it at once", async ({ page, request }) => {
  await openConsole(page, request, "nodes?id=node-local");
  await page.getByRole("button", { name: "Edit node" }).click();
  const edit = page.getByRole("dialog", { name: "Edit node" });
  await edit.getByLabel("Name").fill("core-01-large");
  await edit.getByLabel("Sandboxes at once").fill("6");
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toBeHidden();
  await expect(page.getByRole("heading", { name: "core-01-large", level: 1 })).toBeVisible();
});
