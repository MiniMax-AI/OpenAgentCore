import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("prepares a one-time node command and removes a node after confirmation", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Add node" }).click();
  const add = page.getByRole("dialog", { name: "Add node" });
  await expect(add.getByLabel("One-time enrollment command")).toHaveValue(/enroll_fixture_/);
  await add.getByRole("button", { name: "Close dialog" }).click();

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
  await page.getByLabel("Core address").fill("https://core.example.com");
  await page.getByRole("button", { name: "Save configuration" }).click();

  // The saved specification carries the Runtime read from the console's manifest.
  await expect(page.getByText("c0ffee000000")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add node" })).toBeVisible();
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

  // Another backend starts from its own size, with disks, and this console's Runtime.
  await back.click();
  await back.click();
  await page.getByRole("button", { name: "microsandbox" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  await save.click();
  await expect.poll(() => submitted?.provider).toBe("microsandbox");
  expect(submitted?.resources).toEqual({ cpus: 2, memory_mib: 4096, root_disk_mib: 8192, environment_disk_mib: 8192 });
  expect(submitted?.runtime).toMatchObject({ source_commit: "c0ffee".padEnd(40, "0") });

  // E2B needs its key again and takes no Runtime or disks.
  await back.click();
  await back.click();
  await back.click();
  await page.getByRole("button", { name: "E2B cloud" }).click();
  await page.getByLabel("E2B API key").fill("fixture-private-key");
  await page.getByLabel("Template build").fill("template:94be54a1-138c-4f30-bc87-b13686272dbe");
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  await save.click();
  await expect.poll(() => submitted?.provider).toBe("e2b");
  expect(submitted?.resources).toEqual({ cpus: 2, memory_mib: 2048 });
  expect(submitted?.runtime).toBeUndefined();
});

test("reports a failed sandbox change in a dialog, then reads the state again", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.route("**/core/v1/sandbox/deployment/maintenance", (route) => route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: { message: "The deployment changed.", type: "invalid_request_error", code: "sandbox_deployment_conflict", param: null } }),
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
