import { expect, test } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

test("allows editing a copied request without sending another write or changing its marker", async ({ page, request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
  await page.route("**/console/auth", (route) => route.fulfill({ json: { mode: "authenticated", username: "recovery-admin" } }));
  await page.addInitScript(() => {
    localStorage.setItem("agents-core-web.locale", "en");
    localStorage.setItem(`parsar.introduction.v1:${location.origin}:recovery-admin`, JSON.stringify({ step: 2, dismissed: false }));
  });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await page.getByLabel("Agent name", { exact: true }).fill("Original external request");
  await page.getByLabel("Provider base URL", { exact: true }).fill("https://models.example/v1");
  await page.getByLabel("Core API base URL", { exact: true }).fill(`${fixtureBaseUrl}/v1`);
  const firstCode = await page.locator(".first-request-code pre").innerText();
  const marker = firstCode.match(/"core_home_example": "([^"]+)"/)?.[1];
  expect(marker).toBeTruthy();
  await page.getByRole("button", { name: "Copy request", exact: true }).click();
  await expect(page.getByText("Waiting for your request…", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Agent name", { exact: true })).toBeEnabled();
  await page.getByLabel("Agent name", { exact: true }).fill("Corrected external request");
  await page.getByLabel("Model ID", { exact: true }).fill("fixture/corrected-model");
  await page.getByLabel(/^Model API key/).fill("fixture-memory-only-key");
  await expect(page.getByRole("button", { name: "Run here", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Copy request", exact: true }).click();
  const correctedCode = await page.evaluate(() => navigator.clipboard.readText());
  expect(correctedCode).toContain('"name": "Corrected external request"');
  expect(correctedCode).toContain('"model": "fixture/corrected-model"');
  expect(correctedCode.match(/"core_home_example": "([^"]+)"/)?.[1]).toBe(marker);
  expect(correctedCode).not.toContain("fixture-memory-only-key");
  const before = await (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json() as Array<{ method: string; path: string }>;
  expect(before.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents")).toHaveLength(0);
  const response = await request.post(`${fixtureBaseUrl}/v1/agents`, {
    headers: { "OpenAI-Beta": "agents=v1" },
    data: { name: "Corrected external request", model: "fixture/corrected-model", metadata: { core_home_example: marker } },
  });
  expect(response.status()).toBe(201);
  const agent = await response.json() as { id: string };
  await page.getByRole("button", { name: "Check result", exact: true }).click();
  await expect(page.locator(".first-agent-card")).toContainText(agent.id);
  await expect(page.locator(".first-agent-card")).toContainText("Corrected external request");
  await expect(page.getByLabel("Agent name", { exact: true })).toBeDisabled();
});
