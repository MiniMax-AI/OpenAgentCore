import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole, orcarouterReads, setOrcarouterCatalog } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

/** The key the browser pastes; a fixture credential, never a real one. */
const KEY = "sk-orca-fixture-pasted-key";
/** The catalog the fixture's workspace serves. */
const CHAT_MODELS = ["openai/gpt-5.5", "anthropic/claude-opus-4.8", "deepseek/deepseek-v4-pro"];

test("OrcaRouter is a named provider with both credential entrances and a catalog model list", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model configuration" });
  await section.getByRole("article", { name: "Codex" }).getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const dialog = page.getByRole("dialog", { name: "Set default model configuration for Codex" });

  // The named provider is chosen here, not a free-form base URL.
  await dialog.getByRole("combobox", { name: "Model provider", exact: true }).click();
  await page.getByRole("option", { name: "OrcaRouter", exact: true }).click();
  await expect(dialog.getByText("https://api.orcarouter.ai/v1", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("Base URL")).toHaveCount(0);

  // Both entrances are offered side by side: a pasted key and the connect flow.
  await expect(dialog.getByLabel("OrcaRouter API key")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Connect with OrcaRouter" })).toBeEnabled();
  await page.screenshot({ path: test.info().outputPath("auth-methods.png"), animations: "disabled" });

  // The catalog is read with the operator's key and becomes the model list.
  await dialog.getByLabel("OrcaRouter API key").fill(KEY);
  await expect(dialog.getByRole("status").filter({ hasText: "models from your OrcaRouter workspace" })).toBeVisible();
  const trigger = dialog.getByRole("combobox", { name: "Default model ID" });
  await trigger.click();
  const listbox = page.getByRole("listbox");
  await expect(listbox.getByRole("option")).toHaveCount(CHAT_MODELS.length);
  for (const model of CHAT_MODELS) await expect(listbox.getByText(model, { exact: true })).toBeVisible();
  // A non-text model the catalog carries is never offered as a chat model.
  await expect(listbox.getByText("openai/gpt-image-1", { exact: true })).toHaveCount(0);
  await expect(listbox.getByText("openai/text-embedding-3-large", { exact: true })).toHaveCount(0);
  await page.screenshot({ path: test.info().outputPath("text-model-dropdown.png"), animations: "disabled" });
  await listbox.getByText("openai/gpt-5.5", { exact: true }).click();
  await expect(trigger).toHaveValue(/OpenAI: GPT-5.5/u);

  // Saving writes the gateway's own inference base and the chosen model.
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toBeHidden();
  const codex = section.getByRole("article", { name: "Codex" });
  for (const text of ["https://api.orcarouter.ai/v1", "openai/gpt-5.5"]) await expect(codex).toContainText(text);
  expect(await page.content()).not.toContain(KEY);
});

test("the connect flow redeems a code the operator pastes and fills the same key field", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model configuration" });
  await section.getByRole("article", { name: "Codex" }).getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const dialog = page.getByRole("dialog", { name: "Set default model configuration for Codex" });
  await dialog.getByRole("combobox", { name: "Model provider", exact: true }).click();
  await page.getByRole("option", { name: "OrcaRouter", exact: true }).click();

  const popup = page.waitForEvent("popup");
  await dialog.getByRole("button", { name: "Connect with OrcaRouter" }).click();
  const consent = await popup;
  // The consent URL is the gateway's authorization origin, asks for S256 and never carries a verifier.
  const authorize = new URL(consent.url());
  expect(authorize.origin).toBe("https://www.orcarouter.ai");
  expect(authorize.pathname).toBe("/auth");
  expect(authorize.searchParams.get("callback_url")).toBe("oob");
  expect(authorize.searchParams.get("code_challenge_method")).toBe("S256");
  expect(authorize.searchParams.get("scope")).toBe("api");
  expect(consent.url()).not.toContain("code_verifier");
  await consent.close();

  // A wrong code reports the operator's next step and stores nothing.
  await dialog.getByLabel("Authorization code").fill("not-the-code");
  await dialog.getByRole("button", { name: "Use this code" }).click();
  await expect(dialog.getByRole("alert")).toContainText("already used or expired");

  // The right code produces the same ordinary key the paste entrance would.
  await dialog.getByLabel("Authorization code").fill("fixture-consent-code");
  await dialog.getByRole("button", { name: "Use this code" }).click();
  await expect(dialog.getByLabel("OrcaRouter API key")).toHaveValue(/^sk-orca-/u);
  await expect(dialog.getByRole("status").filter({ hasText: "Connected with OrcaRouter" })).toBeVisible();
  await expect(dialog.getByRole("status").filter({ hasText: "models from your OrcaRouter workspace" })).toBeVisible();
});

test("a refused key and an unreachable catalog are said plainly, never as free text", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model configuration" });
  await section.getByRole("article", { name: "Codex" }).getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const dialog = page.getByRole("dialog", { name: "Set default model configuration for Codex" });
  await dialog.getByRole("combobox", { name: "Model provider", exact: true }).click();
  await page.getByRole("option", { name: "OrcaRouter", exact: true }).click();

  // A key OrcaRouter refuses is named as such; the model list stays a list.
  await dialog.getByLabel("OrcaRouter API key").fill("sk-orca-revoked");
  await expect(dialog.getByRole("alert")).toContainText("rejected this API key");
  await expect(dialog.getByRole("status").filter({ hasText: "built-in verified fallback" })).toBeVisible();
  await expect(dialog.getByRole("combobox", { name: "Default model ID" })).toBeVisible();

  // An outage offers the verified fallback, labelled, and keeps the list.
  await setOrcarouterCatalog(request, true);
  await dialog.getByLabel("OrcaRouter API key").fill(KEY);
  await dialog.getByRole("button", { name: "Refresh the catalog" }).click();
  await expect(dialog.getByRole("status").filter({ hasText: "verified fallback list" })).toBeVisible();
  const trigger = dialog.getByRole("combobox", { name: "Default model ID" });
  await trigger.click();
  const listbox = page.getByRole("listbox");
  await expect(listbox.getByRole("option")).toHaveCount(5);
  await page.keyboard.press("Escape");

  // Every catalog read went to the console's own route with the key in a header.
  const reads = await orcarouterReads(request);
  // Every catalog read went to one same-origin route for this entrance's capability,
  // and the refused key never reached the exchange.
  const catalogs = reads.reads.filter((entry) => entry.startsWith("catalog:"));
  expect(catalogs.length).toBeGreaterThanOrEqual(2);
  expect(catalogs.every((entry) => entry === "catalog:chat")).toBe(true);
  expect(reads.reads).not.toContain("exchange");
  expect(page.url()).not.toContain(reads.key);
});
