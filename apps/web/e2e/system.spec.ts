import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

const KEY = "sk-fixture-default-model-canary";

test("sets, replaces and clears a harness's default model, and keeps its key out of the browser", async ({ page, request }) => {
  // A fresh install: no harness has a default model yet.
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model" });
  const codex = section.getByRole("article", { name: "Codex" });
  const mcode = section.getByRole("article", { name: "MiniMax Code" });
  await expect(codex).toContainText("Enabled");
  await expect(codex).toContainText("Default");
  await expect(codex).toContainText("Not set");
  await expect(section.getByRole("article", { name: "Claude Code" })).toContainText("Not set");
  await expect(mcode).toContainText("Disabled");

  await codex.getByRole("button", { name: "Set the default model for Codex" }).click();
  const set = page.getByRole("dialog", { name: "Set default model for Codex" });
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await set.getByLabel("API key").fill(KEY);
  // Limits are Core's 32-bit whole numbers, and max output needs a context window at least as large.
  await set.getByLabel("Max output tokens").fill("32000");
  await expect(set.getByText("Set a context window at least this large.")).toBeVisible();
  await set.getByLabel("Context window").fill("2147483648");
  await expect(set.getByText("Enter a whole number up to 2147483647.")).toBeVisible();
  await expect(set.getByRole("button", { name: "Save" })).toBeDisabled();
  await set.getByLabel("Context window").fill("200000");
  await set.getByRole("button", { name: "Save" }).click();
  await expect(set).toBeHidden();
  for (const text of ["OpenAI Responses", "https://model.example/v1", "Configured", "200,000", "32,000"]) await expect(codex).toContainText(text);
  expect(await page.content()).not.toContain(KEY);

  // Replacing starts from the saved fields but never from the key; closing the form forgets a typed key.
  await codex.getByRole("button", { name: "Replace the default model for Codex" }).click();
  let replace = page.getByRole("dialog", { name: "Replace default model for Codex" });
  await expect(replace.getByLabel("Base URL")).toHaveValue("https://model.example/v1");
  await expect(replace.getByLabel("API key")).toHaveValue("");
  await expect(replace.getByRole("button", { name: "Save" })).toBeDisabled();
  await replace.getByLabel("API key").fill(KEY);
  await replace.getByRole("button", { name: "Cancel" }).click();
  expect(await page.content()).not.toContain(KEY);
  await codex.getByRole("button", { name: "Replace the default model for Codex" }).click();
  replace = page.getByRole("dialog", { name: "Replace default model for Codex" });
  await expect(replace.getByLabel("API key")).toHaveValue("");
  await replace.getByLabel("Base URL").fill("https://model.example/v2");
  await replace.getByLabel("API key").fill(KEY);
  // Enter saves.
  await replace.getByLabel("API key").press("Enter");
  await expect(replace).toBeHidden();
  await expect(codex).toContainText("https://model.example/v2");

  await codex.getByRole("button", { name: "Clear the default model for Codex" }).click();
  const confirm = page.getByRole("dialog", { name: "Clear default model" });
  await confirm.getByRole("button", { name: "Clear default model" }).click();
  await expect(confirm).toBeHidden();
  await expect(codex).toContainText("Not set");

  // MiniMax Code needs both limits; the form shows Core's reason. A disabled harness may still be configured.
  await mcode.getByRole("button", { name: "Set the default model for MiniMax Code" }).click();
  const limits = page.getByRole("dialog", { name: "Set default model for MiniMax Code" });
  await limits.getByLabel("Base URL").fill("https://model.example/anthropic");
  await limits.getByLabel("API key").fill(KEY);
  await limits.getByRole("button", { name: "Save" }).click();
  await expect(limits.getByRole("alert")).toHaveText("selected harness requires positive model context_window and max_output_tokens");
  await limits.getByRole("button", { name: "Cancel" }).click();
  await expect(mcode).toContainText("Not set");

  expect(await writes(request)).toEqual([
    "PUT /core/v1/harnesses/codex/model-provider",
    "PUT /core/v1/harnesses/codex/model-provider",
    "DELETE /core/v1/harnesses/codex/model-provider",
    "PUT /core/v1/harnesses/mcode/model-provider",
  ]);
  const browserState = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, url: location.href, cookie: document.cookie }));
  expect(browserState).not.toContain(KEY);
});

test("reports a Core without a credential key as a configuration error, without rereading", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true, credentials: "none" });
  const codex = page.getByRole("region", { name: "Default model" }).getByRole("article", { name: "Codex" });
  await codex.getByRole("button", { name: "Set the default model for Codex" }).click();
  const set = page.getByRole("dialog", { name: "Set default model for Codex" });
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await set.getByLabel("API key").fill(KEY);
  const reads: string[] = [];
  page.on("request", (sent) => { if (sent.method() === "GET" && new URL(sent.url()).pathname === "/core/v1/harnesses") reads.push(sent.url()); });
  await set.getByRole("button", { name: "Save" }).click();
  await expect(set.getByRole("alert")).toHaveText("Core has no credential encryption key configured, so it can't store keys. Installer-based installs configure this automatically; for manual deployments, set AGENTS_API_CREDENTIAL_KEY_FILE for Core.");
  await expect(set.getByRole("button", { name: "Save" })).toBeEnabled();
  expect(reads).toEqual([]);
  expect(await writes(request)).toEqual(["PUT /core/v1/harnesses/codex/model-provider"]);
  await set.getByRole("button", { name: "Cancel" }).click();
  await expect(codex).toContainText("Not set");
});

test("reports an unconfirmed save, reads the default models again once and never repeats the write", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true });
  const codex = page.getByRole("region", { name: "Default model" }).getByRole("article", { name: "Codex" });
  await codex.getByRole("button", { name: "Set the default model for Codex" }).click();
  const set = page.getByRole("dialog", { name: "Set default model for Codex" });
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await set.getByLabel("API key").fill(KEY);
  await failNext(request, { method: "PUT", path: "/harnesses/codex/model-provider", status: 500 });
  const reads: string[] = [];
  page.on("request", (sent) => { if (sent.method() === "GET" && new URL(sent.url()).pathname === "/core/v1/harnesses") reads.push(sent.url()); });
  await set.getByRole("button", { name: "Save" }).click();
  await expect(set.getByRole("alert")).toHaveText("Core did not confirm the change. The default models were read again; check them before trying again.");
  await expect.poll(() => reads.length).toBe(1);
  await page.waitForTimeout(500);
  expect(reads).toHaveLength(1);
  expect(await writes(request)).toEqual(["PUT /core/v1/harnesses/codex/model-provider"]);
});
