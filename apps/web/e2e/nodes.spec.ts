import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole, resetFixture, setNode, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("adds a node: host requirements, a sudo command and one without, a countdown, the same command after closing, a new one after expiry, then its own node's registration", async ({ page, request }) => {
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
  // What a Docker host needs for the default command, which installs the node with sudo.
  await expect(add.getByText("Rootful Docker Engine running, its socket owned by the docker group with mode 0660, enforcing CPU and memory limits (cgroup v2)")).toBeVisible();
  await expect(add.getByText("SELinux is not enforcing (otherwise use the no-sudo command)")).toBeVisible();
  await expect(add.getByText("In sudo mode, one Core per host: a host already running a sudo-mode node for another Core is refused.")).toBeVisible();
  await expect(add.getByText("CPUs and memory for at least one sandbox: 2 CPU · 4 GiB; about 2 GB of disk for the Runtime image")).toBeVisible();
  await expect(add.getByText("Reaches https://core.example.com, as do its sandboxes")).toBeVisible();
  await expect(add.getByText("parsar-node joins the docker group, which is equivalent to root on this host.")).toBeVisible();
  await expect(add.getByText(/\/dev\/kvm/)).toHaveCount(0);
  // Preparing a user instead of using sudo waits behind its disclosure.
  await expect(add.getByText("sudo usermod -aG docker NODE_USER")).toBeHidden();
  await add.getByLabel("Sandboxes at once").fill("3");
  const tokenRequest = () => page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith("/core/v1/sandbox/enrollment-tokens"));
  const issued = tokenRequest();
  await add.getByRole("button", { name: "Generate command" }).click();
  // Docker never suspends, so it retains exactly the sandboxes it runs.
  expect((await issued).postDataJSON()).toEqual({ max_active: 3, max_retained: 3 });
  const field = add.getByLabel("One-time enrollment command", { exact: true });
  await expect(field).toHaveValue(/enroll_fixture_/);
  // The token goes on stdin to the checked installer, run with sudo unless the shell is root.
  await expect(field).toHaveValue(/^ \(umask 077;.*\|\| s=sudo\n/);
  await expect(field).toHaveValue(/\| \$s python3 "\$d\/node-install\.pyz" --enrollment-token-stdin /);
  // It downloads from, and names as its source, the public URL, not the loopback address this browser uses.
  await expect(field).toHaveValue(/curl [^\n]* 'https:\/\/core\.example\.com\/node-install\/node-install\.pyz' /);
  await expect(field).toHaveValue(/ --source-url 'https:\/\/core\.example\.com' --core-url 'https:\/\/core\.example\.com' /);
  await expect(add.getByText("If the command is interrupted or the download stalls, run the same command again: the download resumes.")).toBeVisible();
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
  const problem = add.getByRole("alert").filter({ hasText: "Check the log on the host:" });
  await expect(problem).toContainText("Not connected yet");
  await expect(problem).toContainText("sudo journalctl -u parsar-node-7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f.service");
  // Without sudo: what that user needs and the same command without sudo. Once that one is copied, the log
  // hint names its user service, and the system service in case root ran it.
  await add.getByText("No sudo on this host?").click();
  await expect(add.getByText("sudo usermod -aG docker NODE_USER")).toBeVisible();
  const userCommand = add.getByLabel("One-time enrollment command without sudo", { exact: true });
  await expect(userCommand).toHaveValue(/EXIT\ncurl/);
  await expect(userCommand).toHaveValue(/\| python3 "\$d\/node-install\.pyz" --enrollment-token-stdin /);
  await expect(problem).not.toContainText("journalctl --user");
  await add.getByRole("button", { name: "Copy command without sudo" }).click();
  await expect(problem).toContainText("journalctl --user -u parsar-node-7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f.service");
  await expect(problem).toContainText("If root ran it, it is a system service:sudo journalctl -u parsar-node-7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f.service");
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

test("issues no command before the installation is read, for a loopback public URL or without node files, and sees a fix on reopening", async ({ page, request }) => {
  // Until the installation is read, and while it can't be, nothing is issued: the read decides.
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/core/v1/installation", async (route) => {
    await held;
    await route.fulfill({ status: 500, json: { error: { message: "Unavailable.", type: "server_error", code: null, param: null } } });
  });
  await openConsole(page, request, "nodes", { installation: "local" });
  await page.getByRole("button", { name: "Add node" }).click();
  const add = page.getByRole("dialog", { name: "Add node" });
  await expect(add.getByRole("status")).toHaveText("Checking this installation's public URL…");
  await expect(add.getByRole("button", { name: "Generate command" })).toHaveCount(0);
  release();
  await expect(add.getByRole("alert")).toContainText("The installation couldn't be read, so no command can be issued.");
  await expect(add.getByRole("button", { name: "Generate command" })).toHaveCount(0);
  await page.unroute("**/core/v1/installation");
  await add.getByRole("button", { name: "Try again" }).click();
  // Nodes on other machines can't reach a loopback public_url.
  await expect(add.getByRole("status")).toHaveText("Nodes need an HTTPS public URL that other machines and their sandboxes can reach: set public_url in config.json and run parsar apply");
  await expect(add.getByRole("button", { name: "Generate command" })).toHaveCount(0);
  await add.getByRole("button", { name: "Close dialog" }).click();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeDisabled();
  await expect(page.getByText("Add node is unavailable while the public address is local only.")).toBeVisible();

  // Applying a public address is an external change. Refresh the page's cached
  // installation before Add node becomes available again, then check a thin bundle.
  await openConsole(page, request, "nodes", { nodeArtifacts: [] });
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await page.getByRole("button", { name: "Add node" }).click();
  await expect(add.getByRole("status")).toHaveText("This console has no node files for Docker. Install Core from the offline bundle, or add the release artifacts and rerun ./install.sh.");
  await expect(add.getByRole("button", { name: "Generate command" })).toHaveCount(0);
  expect(await writes(request)).toEqual([]);
  // Rerunning ./install.sh adds them: reopening reads the console again, without a reload.
  await add.getByRole("button", { name: "Close dialog" }).click();
  await resetFixture(request);
  await page.getByRole("button", { name: "Add node" }).click();
  await expect(add.getByRole("button", { name: "Generate command" })).toBeVisible();
});

test("removes a node after confirmation", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Remove edge-03" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove node" });
  await confirm.getByRole("button", { name: "Confirm removal" }).click();
  await expect(confirm).toBeHidden();
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).not.toContainText("edge-03");
  // The host still runs the node until it is uninstalled there: with sudo, or as the user that installed it.
  const cleanup = page.getByRole("dialog", { name: "Clean up the host" });
  await expect(cleanup.getByLabel("Uninstall command", { exact: true })).toHaveValue(/\| s=sudo\ncurl [^\n]* 'https:\/\/core\.example\.com\/node-install\/node-install\.pyz' [^]*\n\$s python3 "\$d\/node-install\.pyz" --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f'\)$/);
  await cleanup.getByText("Installed without sudo?").click();
  await expect(cleanup.getByLabel("Uninstall command without sudo", { exact: true })).toHaveValue(/\npython3 "\$d\/node-install\.pyz" --uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f'\)$/);
  // Nothing to force for a node on the current address; closing leaves focus on the page, as the row is gone.
  await expect(cleanup.getByText("Old Core address gone?")).toHaveCount(0);
  await cleanup.getByRole("button", { name: "Done" }).click();
  await expect(cleanup).toBeHidden();
  await expect(page.getByRole("heading", { name: "Nodes", level: 1 })).toBeFocused();
});

test("marks a node on an old Core address in its row, beside each node's limit", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { installation: "stale" });
  const row = page.getByRole("row", { name: /core-01/ });
  await expect(row).toContainText("Old address");
  await expect(row).toContainText("Remove and add again");
  await expect(row).not.toContainText("Available");
  // Docker nodes show their limit too.
  await expect(row).toContainText("5 / 8");
});

test("gives the host's uninstall command even when the installation must be read again", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.route("**/core/v1/installation", (route) => route.fulfill({ status: 500, json: { error: { message: "Unavailable.", type: "server_error", code: null, param: null } } }));
  await page.getByRole("button", { name: "Remove edge-03" }).click();
  await page.getByRole("dialog", { name: "Remove node" }).getByRole("button", { name: "Confirm removal" }).click();
  const cleanup = page.getByRole("dialog", { name: "Clean up the host" });
  await expect(cleanup.getByRole("alert")).toContainText("The installation couldn't be read, so no command can be issued.");
  await page.unroute("**/core/v1/installation");
  await cleanup.getByRole("button", { name: "Try again" }).click();
  await expect(cleanup.getByLabel("Uninstall command", { exact: true })).toHaveValue(/'https:\/\/core\.example\.com\/node-install\/node-install\.pyz'/);
});

test("sets up own-machine sandboxes page by page, with the Runtime from the distribution", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await expect(page.getByRole("heading", { name: "Where should sandboxes run?" })).toBeVisible();
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "microsandbox Recommended" }).click();
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

test("preselects microsandbox and asks once before switching to Docker", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await page.getByRole("button", { name: "Own machines" }).click();
  const microsandbox = page.getByRole("button", { name: "microsandbox Recommended" });
  const docker = page.getByRole("button", { name: "Docker", exact: true });
  const confirm = page.getByRole("dialog", { name: "Use Docker instead of microsandbox?" });
  const sizeStep = page.getByRole("heading", { name: "How big is each sandbox?" });
  await expect(microsandbox).toHaveAttribute("aria-pressed", "true");

  // Keeping microsandbox is the default action; it leaves microsandbox selected.
  await docker.click();
  const keep = confirm.getByRole("button", { name: "Keep microsandbox" });
  await expect(keep).toBeFocused();
  await keep.click();
  await expect(confirm).toBeHidden();
  await expect(microsandbox).toHaveAttribute("aria-pressed", "true");

  // Confirmed, Docker is selected and the wizard does not ask again.
  await docker.click();
  await confirm.getByRole("button", { name: "Use Docker" }).click();
  await expect(sizeStep).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(docker).toHaveAttribute("aria-pressed", "true");
  await docker.click();
  await expect(sizeStep).toBeVisible();
  await expect(confirm).toHaveCount(0);
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
    owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 0, pending: 0 }, specification: current, specification_digest: "e".repeat(64),
    suspension: null };
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
  // Core's answer is lost, so the change may have been saved.
  await page.route("**/core/v1/sandbox/deployment/maintenance", (route) => route.fulfill({
    status: 503,
    contentType: "application/json",
    body: JSON.stringify({ error: { message: "Unavailable.", type: "server_error", code: null, param: null } }),
  }));
  await page.getByRole("button", { name: "Enter maintenance to change provider" }).click();
  const failed = page.getByRole("dialog", { name: "Couldn't confirm the sandbox change" });
  await failed.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(failed).toBeHidden();
  await expect(page.getByRole("button", { name: "Enter maintenance to change provider" })).toBeEnabled();
});

test("keeps the page usable when Core refuses a sandbox change, and shows Core's reason", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await failNext(request, { method: "POST", path: "/sandbox/deployment", status: 403, message: "This console is read-only." });
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "microsandbox Recommended" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  const save = page.getByRole("button", { name: "Save configuration" });
  await save.click();
  // A clear refusal changed nothing: no "couldn't confirm" dialog, and the same page to try again.
  await expect(page.getByText("This console is read-only.")).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // One code covers several reasons, so a conflict shows Core's own.
  await failNext(request, { method: "POST", path: "/sandbox/deployment", status: 409, code: "sandbox_deployment_conflict", message: "Another administrator changed the deployment; it is now at generation 2." });
  await save.click();
  await expect(page.getByText("Another administrator changed the deployment; it is now at generation 2.")).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await save.click();
  await expect(page.getByText("c0ffee000000")).toBeVisible();
});

test("renames a node and sets how many sandboxes run on it at once", async ({ page, request }) => {
  await openConsole(page, request, "nodes?id=node-local");
  // A Docker node shows its limit too, and the edit shows what the host holds.
  const capacity = page.getByRole("region", { name: "Capacity" });
  await expect(capacity).toContainText("Active / limit");
  await expect(capacity).toContainText("5 / 8");
  await page.getByRole("button", { name: "Edit node" }).click();
  const edit = page.getByRole("dialog", { name: "Edit node" });
  await expect(edit.getByText("Host: 16 CPU · 64 GiB. Each sandbox: 2 CPU · 4 GiB. Suggested: at most 8 at once.")).toBeVisible();
  await edit.getByLabel("Name").fill("core-01-large");
  await edit.getByLabel("Sandboxes at once").fill("6");
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toBeHidden();
  await expect(page.getByRole("heading", { name: "core-01-large", level: 1 })).toBeVisible();
  await expect(capacity).toContainText("5 / 6");
});
