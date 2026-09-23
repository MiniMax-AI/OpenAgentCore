import { expect, test } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

test.beforeEach(async ({ page, request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.addInitScript(() => localStorage.setItem("agents-core-web.locale", "en"));
  await page.route("**/console/auth", (route) => route.fulfill({ json: { mode: "authenticated", username: "keyboard-admin" } }));
  await page.route("**/console/config", (route) => route.fulfill({ json: { api_keys: true, sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) } }));
  await page.route("**/console/api-keys", (route) => route.fulfill({ json: { data: [{
    id: "fb533e99-524f-4e44-94bc-8f6e571646a7", name: "Existing key", prefix: "pc_fixture",
    created_at: "2026-09-24T00:00:00Z", revoked_at: null,
  }] } }));
});

test("moves focus from disappearing step actions to the new heading and retains chapter navigation focus", async ({ page }) => {
  await page.goto("/");
  const accessHeading = page.getByRole("heading", { name: "Keep your sign-in details.", exact: true });
  await expect(accessHeading).toBeVisible();
  await expect(accessHeading).not.toBeFocused();
  const continueButton = page.getByRole("button", { name: "I've saved it. Continue", exact: true });
  await expect(continueButton).toBeEnabled();
  await continueButton.focus();
  await page.keyboard.press("Enter");
  const machineHeading = page.getByRole("heading", { name: "Connect your own machine.", exact: true });
  await expect(machineHeading).toBeFocused();
  await expect(page.getByRole("button", { name: "Refresh sandbox state", exact: true })).toBeEnabled();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Refresh sandbox state", exact: true })).toBeFocused();

  await page.getByRole("button", { name: "Skip for now", exact: true }).focus();
  await page.keyboard.press("Enter");
  const requestHeading = page.getByRole("heading", { name: "Make your first API request.", exact: true });
  await expect(requestHeading).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Agent name", { exact: true })).toBeFocused();

  const machineChapter = page.getByRole("button", { name: /Your machines/ });
  await machineChapter.focus();
  await page.keyboard.press("Enter");
  await expect(machineHeading).toBeVisible();
  await expect(machineChapter).toBeFocused();
  await page.getByRole("button", { name: "Continue to the API", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(requestHeading).toBeFocused();

  await page.getByRole("button", { name: "Back", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(machineHeading).toBeFocused();
  await page.getByRole("button", { name: "Back", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(accessHeading).toBeFocused();
});

test("does not move retained navigation focus when polling discovers a newly created Agent", async ({ page, request }) => {
  await page.goto("/");
  const chapter = page.getByRole("button", { name: /Your first Agent/ });
  await expect(chapter).toBeEnabled();
  await chapter.focus();
  await page.keyboard.press("Enter");
  await expect(chapter).toBeFocused();
  await page.getByLabel("Set a model provider for this Agent", { exact: true }).uncheck();
  const code = await page.locator(".first-request-code pre").innerText();
  const marker = code.match(/"core_home_example": "([^"]+)"/)?.[1];
  expect(marker).toBeTruthy();
  await chapter.focus();
  const created = await request.post(`${fixtureBaseUrl}/v1/agents`, {
    headers: { "OpenAI-Beta": "agents=v1" },
    data: { name: "Keyboard Agent", model: "fixture/keyboard-model", metadata: { core_home_example: marker } },
  });
  expect(created.status()).toBe(201);
  const agent = await created.json() as { id: string };
  await expect(page.locator(".first-agent-card")).toContainText(agent.id);
  await expect(chapter).toBeFocused();
  await expect(page.getByRole("heading", { name: "Make your first API request.", exact: true })).not.toBeFocused();
});
