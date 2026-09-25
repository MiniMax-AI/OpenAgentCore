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

test("preserves saved resources and Runtime, and clears provider-specific fields on switching", async ({ page, request }) => {
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
  await page.route("**/node-install/manifest.json", (route) => route.fulfill({ json: { platform: "linux/amd64", source_commit: "e".repeat(40),
    images: { runtime: `sha256:${"f".repeat(64)}` }, image_manifest_digests: { runtime: `sha256:${"1".repeat(64)}` },
    runtime_ref: `parsar-core-runtime@sha256:${"1".repeat(64)}`, microsandbox: { runtime_sha256: "2".repeat(64), firmware_sha256: "3".repeat(64) } } }));
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Change provider or resources" }).click();
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByRole("combobox", { name: "Sandbox provider", exact: true }).selectOption("docker");
  await page.getByText("Advanced sandbox resources", { exact: true }).click();
  await expect(page.getByLabel("CPU cores per sandbox", { exact: true })).toHaveValue("7");
  await page.getByRole("button", { name: "Save provider and stay in maintenance" }).click();
  await expect.poll(() => submitted?.resources).toEqual(current.resources);
  expect(submitted?.runtime).toEqual(runtime);
  expect(submitted?.specification).toBeUndefined();
  await page.getByRole("combobox", { name: "Sandbox provider", exact: true }).selectOption("microsandbox");
  await expect(page.getByLabel("CPU cores per sandbox", { exact: true })).toHaveValue("2");
  await page.getByRole("button", { name: "Save provider and stay in maintenance" }).click();
  await expect.poll(() => submitted?.provider).toBe("microsandbox");
  expect(submitted?.resources).toEqual({ cpus: 2, memory_mib: 4096, root_disk_mib: 8192, environment_disk_mib: 8192 });
  expect(submitted?.runtime).toMatchObject({ source_commit: "e".repeat(40) });
  await page.getByLabel("Where to run sandboxes").selectOption("direct");
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-key");
  await page.getByLabel("Immutable Runtime template", { exact: true }).fill("template:94be54a1-138c-4f30-bc87-b13686272dbe");
  await page.getByRole("button", { name: "Save provider and stay in maintenance" }).click();
  await expect.poll(() => submitted?.provider).toBe("e2b");
  expect(submitted?.resources).toEqual({ cpus: 2, memory_mib: 2048 });
  expect(submitted?.runtime).toBeUndefined();
});
