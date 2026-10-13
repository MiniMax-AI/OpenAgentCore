import { expect, test } from "@playwright/test";
import { expectManagementBoundary, openConsole, setDeployment } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

const initial = { id: "ddda6e9f-c15c-423e-9a95-c93b8fbe5a20", adapter: "test-storage", parameters: { root: "/workspaces" }, tenant_capacity_mib: 4096 };

test("storage capacity has one settings home and preserves immutable configuration without sending credentials", async ({ page, request }) => {
  let selected = { ...initial };
  const saves: Record<string, unknown>[] = [];
  await page.route("**/core/v1/workspace-storage", async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON();
      saves.push(body);
      selected = { ...selected, tenant_capacity_mib: body.tenant_capacity_mib };
    }
    await route.fulfill({ json: selected });
  });
  await openConsole(page, request, "settings?id=storage");
  await expect(page.getByRole("navigation").getByRole("button", { name: "Workspace storage", exact: true })).toHaveAttribute("aria-current", "page");
  await page.getByRole("button", { name: "Edit tenant capacity", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Edit tenant capacity" });
  await expect(dialog.getByLabel("Credential", { exact: false })).toHaveCount(0);
  await dialog.getByLabel("Tenant capacity (MiB)").fill("8192");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(saves).toEqual([{ ...initial, tenant_capacity_mib: 8192 }]);
  await expect(page.getByText("8,192", { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText("8,192", { exact: true })).toBeVisible();
});

test("new storage selection validates objects, retains retry identity, and never redisplays server secrets", async ({ page, request }) => {
  const saves: Record<string, unknown>[] = [];
  const secret = "fixture-write-only-storage-secret";
  let selected = { ...initial };
  await page.route("**/core/v1/workspace-storage", async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON();
      saves.push(body);
      if (saves.length === 1) {
        await route.fulfill({ status: 500, json: { error: { code: "internal_error", message: secret } } });
        return;
      }
      const { credential: _credential, ...publicSelection } = body;
      selected = publicSelection;
    }
    await route.fulfill({ json: selected });
  });
  await openConsole(page, request, "settings?id=storage");
  await page.getByRole("button", { name: "Select new configuration" }).click();
  const dialog = page.getByRole("dialog", { name: "Select new configuration" });
  await dialog.getByLabel("Parameters (JSON)").fill("[]");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("valid JSON objects");
  expect(saves).toHaveLength(0);
  await dialog.getByLabel("Parameters (JSON)").fill('{"root":"/next"}');
  await dialog.getByLabel("Credential (JSON, write-only)").fill(JSON.stringify({ token: secret }));
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(secret);
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(saves).toHaveLength(2);
  expect(saves[1]).toEqual(saves[0]);
  expect(saves[0].id).not.toBe(initial.id);
  expect(saves[0].credential).toEqual({ token: secret });
  await page.getByRole("button", { name: "Select new configuration" }).click();
  await expect(dialog.getByLabel("Credential (JSON, write-only)")).toHaveValue("");
});

test("capacity editor refuses to select an old configuration after observing a replacement", async ({ page, request }) => {
  let selected = { ...initial };
  const saves: Record<string, unknown>[] = [];
  await page.route("**/core/v1/workspace-storage", async (route) => {
    if (route.request().method() === "PUT") {
      saves.push(route.request().postDataJSON());
      await route.fulfill({ status: 500, json: { error: { code: "internal_error", message: "Try again" } } });
      return;
    }
    await route.fulfill({ json: selected });
  });
  await openConsole(page, request, "settings?id=storage");
  await page.getByRole("button", { name: "Edit tenant capacity", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Edit tenant capacity" });
  await dialog.getByLabel("Tenant capacity (MiB)").fill("8192");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  selected = { ...initial, id: "11a66e9f-c15c-423e-9a95-c93b8fbe5a20" };
  await dialog.getByRole("button", { name: "Refresh current selection" }).click();
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  expect(saves).toHaveLength(1);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Edit tenant capacity", exact: true }).click();
  await dialog.getByLabel("Tenant capacity (MiB)").fill("8192");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  expect(saves[1].id).toBe(selected.id);
});

test("settings content width does not constrain reset and node dialog backdrops", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await openConsole(page, request, "settings?id=sandbox");
  for (const action of ["Reset deployment", "Edit node"]) {
    await page.getByRole("button", { name: action, exact: true }).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const backdrop = page.locator(".modal-backdrop");
    const bounds = await backdrop.boundingBox();
    expect(bounds?.x).toBe(0);
    expect(bounds?.y).toBe(0);
    expect(bounds?.width).toBe(1440);
    expect(bounds?.height).toBe(1000);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
  }
});


test("reset progress actions stay inside settings on narrow screens", async ({ page, request }) => {
  await page.setViewportSize({ width: 375, height: 844 });
  await openConsole(page, request, "settings?id=sandbox");
  await setDeployment(request, { resources: { allocations: 1, pending: 0 }, reset: { clear: "auto", requested_at: "2026-10-13T08:00:00Z", deadline_at: "2026-10-13T09:00:00Z", forced_at: null, remaining: { busy: 1, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } } });
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  const region = page.getByRole("region", { name: "Reset in progress" });
  await expect(region).toBeVisible();
  const bounds = (await region.boundingBox())!;
  for (const name of ["Force reset now", "Cancel reset"]) {
    const action = region.getByRole("button", { name, exact: true });
    const box = (await action.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(bounds.x);
    expect(box.x + box.width).toBeLessThanOrEqual(bounds.x + bounds.width + 1);
    await action.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
  }
});
