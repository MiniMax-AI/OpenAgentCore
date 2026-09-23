import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";

// Runner traces record HTTP bodies. This file deliberately submits transient
// bearer values, so it must never produce a trace artifact.
test.use({ trace: "off" });

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

interface FixtureRequest {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

async function resetFixture(request: APIRequestContext): Promise<void> {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/reset`);
  expect(response.ok()).toBe(true);
}

async function fixtureRequests(request: APIRequestContext): Promise<FixtureRequest[]> {
  const response = await request.get(`${fixtureBaseUrl}/__fixture/requests`);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<FixtureRequest[]>;
}

async function openAgents(page: Page, request: APIRequestContext): Promise<void> {
  await resetFixture(request);
  await page.goto("/");
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
}

async function openAdvancedSessionSettings(dialog: Locator): Promise<void> {
  const toggle = dialog.getByRole("button", { name: /Advanced settings/ });
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
}

async function fillWriteOnlyBearer(dialog: Locator): Promise<void> {
  const input = dialog.locator('input[type="password"]');
  await expect(input).toHaveCount(1);
  await input.evaluate((element) => {
    (element as HTMLInputElement).value = globalThis.crypto.randomUUID().replaceAll("-", "");
  });
}

test.describe("Vault capability discovery", () => {
  for (const status of [404, 405]) {
    test(`hides Vault controls when the connected Core returns ${status}`, async ({ page, request }) => {
      await resetFixture(request);
      await page.route("**/v1/vaults?*", async (route) => {
        if (route.request().method() === "GET" && new URL(route.request().url()).pathname === "/v1/vaults") {
          await route.fulfill({
            status,
            json: { error: { code: status === 404 ? "not_found" : "method_not_allowed", message: "Unavailable." } },
          });
          return;
        }
        await route.continue();
      });
      await page.goto("/");

      await expect(page.getByRole("button", { name: "Vaults", exact: true })).toHaveCount(0);
      await page.getByRole("button", { name: "System", exact: true }).click();
      await expect(page.locator(".system-page")).not.toContainText("Vaults");
    });
  }
});

test("keeps Vault content aligned with the page header without narrow-screen clipping", async ({ page, request }) => {
  await resetFixture(request);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto("/");
  await page.getByRole("button", { name: "Vaults", exact: true }).click();

  const desktop = await page.locator(".vaults-page").evaluate((pageElement) => {
    const title = pageElement.querySelector(".page-header h1")?.getBoundingClientRect();
    const contentElement = pageElement.querySelector<HTMLElement>(".vaults-content");
    const note = contentElement?.querySelector(".vault-security-note")?.getBoundingClientRect();
    const page = pageElement.getBoundingClientRect();
    return {
      contentOverflowY: contentElement ? getComputedStyle(contentElement).overflowY : null,
      pageRight: Math.round(page.right),
      titleLeft: Math.round(title?.left ?? -1),
      noteLeft: Math.round(note?.left ?? -2),
      noteRight: Math.round(note?.right ?? -3),
    };
  });
  expect(desktop.contentOverflowY).toBe("auto");
  expect(desktop.noteLeft).toBe(desktop.titleLeft);
  expect(desktop.pageRight - desktop.noteRight).toBeGreaterThanOrEqual(23);
  expect(desktop.pageRight - desktop.noteRight).toBeLessThanOrEqual(24);

  await page.setViewportSize({ width: 375, height: 720 });
  const narrow = await page.locator(".vaults-page").evaluate((pageElement) => {
    const title = pageElement.querySelector(".page-header h1")?.getBoundingClientRect();
    const note = pageElement.querySelector(".vault-security-note")?.getBoundingClientRect();
    const createButton = pageElement.querySelector<HTMLButtonElement>(".page-actions .button.primary")?.getBoundingClientRect();
    const page = pageElement.getBoundingClientRect();
    return {
      pageClientWidth: pageElement.clientWidth,
      pageScrollWidth: pageElement.scrollWidth,
      pageRight: Math.round(page.right),
      titleLeft: Math.round(title?.left ?? -1),
      noteLeft: Math.round(note?.left ?? -2),
      createButtonRight: Math.round(createButton?.right ?? -3),
    };
  });
  expect(narrow.pageScrollWidth).toBeLessThanOrEqual(narrow.pageClientWidth);
  expect(narrow.noteLeft).toBe(narrow.titleLeft);
  expect(narrow.pageRight - narrow.createButtonRight).toBeGreaterThanOrEqual(11);
  expect(narrow.pageRight - narrow.createButtonRight).toBeLessThanOrEqual(12);
});

test("shows a fixed 503 storage error, clears the token, and does not retry the write", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: "Vaults", exact: true }).click();
  await page.getByRole("button", { name: "New Vault" }).click();
  const vaultDialog = page.getByRole("dialog", { name: "Create a Vault" });
  await vaultDialog.getByLabel("Name").fill("Unavailable storage");
  await vaultDialog.getByRole("button", { name: "Create Vault" }).click();

  let credentialWrites = 0;
  await page.route("**/v1/vaults/*/credentials", async (route) => {
    if (route.request().method() === "POST") {
      credentialWrites += 1;
      await route.fulfill({
        status: 503,
        json: {
          error: {
            code: "credential_storage_unavailable",
            message: "Private fixture detail that the Web must not expose.",
          },
        },
      });
      return;
    }
    await route.continue();
  });

  const vaultCard = page.locator(".vault-card").filter({ hasText: "Unavailable storage" });
  await vaultCard.getByRole("button", { name: "Credential", exact: true }).click();
  const credentialDialog = page.getByRole("dialog", { name: "Add static bearer Credential" });
  // Let the modal finish its initial focus before filling another field.
  await expect(credentialDialog.getByLabel("Name")).toBeFocused();
  await credentialDialog.getByLabel("Name").fill("Unavailable MCP");
  await credentialDialog.getByLabel("Exact MCP server URL").fill("https://mcp.example/unavailable");
  await fillWriteOnlyBearer(credentialDialog);
  await credentialDialog.getByRole("button", { name: "Create Credential" }).click();

  await expect(credentialDialog.getByRole("alert")).toContainText("Credential encryption is not configured on this Core");
  await expect(credentialDialog).not.toContainText("Private fixture detail");
  await expect(credentialDialog.locator('input[type="password"]')).toHaveValue("");
  expect(credentialWrites).toBe(1);
});

test("creates, replaces, uses, and deletes a write-only Vault Credential", async ({ page, request }) => {
  const mcpURL = "https://mcp.example/vault-tools";
  await openAgents(page, request);

  const vaultsNavigation = page.getByRole("button", { name: "Vaults", exact: true });
  await expect(vaultsNavigation).toBeVisible();
  await vaultsNavigation.click();
  await page.getByRole("button", { name: "New Vault" }).click();
  const vaultDialog = page.getByRole("dialog", { name: "Create a Vault" });
  await vaultDialog.getByLabel("Name").fill("Runtime credentials");
  await vaultDialog.getByRole("button", { name: "Create Vault" }).click();

  const vaultCard = page.locator(".vault-card").filter({ hasText: "Runtime credentials" });
  await expect(vaultCard).toBeVisible();
  await vaultCard.getByRole("button", { name: "Credential", exact: true }).click();
  const credentialDialog = page.getByRole("dialog", { name: "Add static bearer Credential" });
  await expect(credentialDialog.getByLabel("Name")).toBeFocused();
  await credentialDialog.getByLabel("Name").fill("Private docs MCP");
  await credentialDialog.getByLabel("Exact MCP server URL").fill(mcpURL);
  await fillWriteOnlyBearer(credentialDialog);
  await credentialDialog.getByRole("button", { name: "Create Credential" }).click();
  await expect(vaultCard).toContainText("Private docs MCP");
  await expect(vaultCard).toContainText("token hidden");

  let requests = await fixtureRequests(request);
  const credentialWrite = requests.find((entry) => entry.method === "POST" && /\/v1\/vaults\/[^/]+\/credentials$/u.test(entry.path));
  expect(credentialWrite?.body?.auth).toMatchObject({ type: "static_bearer", mcp_server_url: mcpURL, token_present: true });
  expect(credentialWrite?.body?.auth).not.toHaveProperty("token");

  await vaultCard.getByRole("button", { name: "Replace token for Private docs MCP", exact: true }).click();
  const replacementDialog = page.getByRole("dialog", { name: "Replace token · Private docs MCP" });
  await fillWriteOnlyBearer(replacementDialog);
  await replacementDialog.getByRole("button", { name: "Replace token", exact: true }).click();
  await expect(replacementDialog).toHaveCount(0);
  requests = await fixtureRequests(request);
  const replacementWrite = requests.find((entry) => entry.method === "POST" && /\/v1\/vaults\/[^/]+\/credentials\/[^/]+$/u.test(entry.path));
  expect(replacementWrite?.body?.auth).toEqual({ type: "static_bearer", token_present: true });
  expect(replacementWrite?.body?.auth).not.toHaveProperty("token");

  await page.getByRole("button", { name: "Agents" }).click();
  await page.getByRole("button", { name: /^Create agent/ }).click();
  await page.getByLabel("Name").fill("Credentialed MCP Agent");
  await page.getByLabel("Model").selectOption({ label: "Custom model ID…" });
  await page.getByLabel("Custom model ID").fill("fixture/credential-model");
  await page.getByRole("button", { name: "Add HTTP MCP" }).click();
  const mcpCard = page.locator(".agent-tool-card").filter({ hasText: "HTTP MCP" }).first();
  await mcpCard.getByLabel("Server label").fill("private-docs");
  await mcpCard.getByLabel("Authentication").selectOption({ label: `Private docs MCP · ${mcpURL}` });
  await expect(mcpCard.getByLabel("Server URL")).toHaveValue(mcpURL);
  await expect(mcpCard.getByLabel("Server URL")).toHaveAttribute("readonly", "");
  await page.getByRole("button", { name: "Save Agent definition" }).click();
  await expect(page.getByRole("status")).toContainText("Agent definition saved as");
  await expect(page.getByRole("button", { name: "Start Session" })).toBeEnabled();
  await page.getByRole("button", { name: "Start Session" }).click();

  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await openAdvancedSessionSettings(sessionDialog);
  await expect(sessionDialog.getByRole("heading", { name: "Tools & Vaults" })).toBeVisible();
  await expect(sessionDialog).toContainText("Private docs MCP · Runtime credentials");
  await sessionDialog.getByRole("textbox", { name: /^First message\b/u }).fill("Find the documentation available through this Credential.");
  await sessionDialog.getByRole("button", { name: "Create Session" }).click();
  await expect(page.locator(".toast-region:not(.toast-region-assertive)")).toContainText("Session created with initial input");

  requests = await fixtureRequests(request);
  const vaultCreate = requests.find((entry) => entry.method === "POST" && entry.path === "/v1/vaults");
  const vaultId = requests.find((entry) => entry.method === "POST" && /\/v1\/vaults\/[^/]+\/credentials$/u.test(entry.path))?.path.split("/")[3];
  expect(vaultCreate).toBeTruthy();
  expect(vaultId).toMatch(/^[0-9a-f-]{36}$/u);
  const agentCreate = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents").at(-1);
  const credentialId = (agentCreate?.body?.tools as Array<Record<string, unknown>> | undefined)?.[0]?.credential_id;
  expect(credentialId).toMatch(/^[0-9a-f-]{36}$/u);
  const sessionCreate = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions").at(-1);
  expect(sessionCreate?.body?.vault_ids).toEqual([vaultId]);
  expect(JSON.stringify(requests)).not.toContain('"token":');

  await page.getByRole("button", { name: "Vaults", exact: true }).click();
  const currentVaultCard = page.locator(".vault-card").filter({ hasText: "Runtime credentials" });
  await currentVaultCard.getByRole("button", { name: "Delete Private docs MCP", exact: true }).click();
  const deleteCredentialDialog = page.getByRole("dialog", { name: "Delete Credential?" });
  await deleteCredentialDialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(currentVaultCard).not.toContainText("Private docs MCP");
  await currentVaultCard.getByRole("button", { name: "Delete Runtime credentials", exact: true }).click();
  const deleteVaultDialog = page.getByRole("dialog", { name: "Delete Vault?" });
  await deleteVaultDialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No Vaults" })).toBeVisible();

  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE" && /\/v1\/vaults\/[^/]+\/credentials\/[^/]+$/u.test(entry.path))).toHaveLength(1);
  expect(requests.filter((entry) => entry.method === "DELETE" && /^\/v1\/vaults\/[^/]+$/u.test(entry.path))).toHaveLength(1);
  expect(JSON.stringify(requests)).not.toContain('"token":');
});
