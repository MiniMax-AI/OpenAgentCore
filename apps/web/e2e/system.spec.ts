import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

const KEY = "sk-fixture-default-model-canary";

test("sets, replaces and clears a harness's default model configuration, and keeps its key out of the browser", async ({ page, request }) => {
  // A fresh install: no harness has a default model configuration yet.
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model configuration" });
  const codex = section.getByRole("article", { name: "Codex" });
  const mcode = section.getByRole("article", { name: "MiniMax Code" });
  await expect(codex).toContainText("Enabled");
  await expect(codex).toContainText("Default");
  await expect(codex).toContainText("Not set");
  await expect(section.getByRole("article", { name: "Claude Code" })).toContainText("Not set");
  await expect(mcode).toContainText("Disabled");

  await codex.getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const set = page.getByRole("dialog", { name: "Set default model configuration for Codex" });
  await expect(set.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText("OpenAI Responses");
  await set.getByRole("combobox", { name: "Protocol", exact: true }).click();
  await expect(page.getByRole("option")).toHaveText(["OpenAI Responses"]);
  await page.screenshot({ path: test.info().outputPath("provider-protocol-options.png"), animations: "disabled" });
  await page.getByRole("option", { name: "OpenAI Responses", exact: true }).click();
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await set.getByLabel("API key").fill(KEY);
  await set.getByLabel("Default model ID").fill("fixture-model");
  await expect(set).toContainText("Used for new Sessions when the application does not specify a model.");
  // Every rejected URL stays local; Enter cannot bypass validation or write a secret.
  for (const url of ["http://model.example/v1", "ftp://model.example", "https://user:password@model.example/v1", "https://@model.example/v1", "https:model.example/v1", "https://model.example/v1?token=x", "https://model.example/v1#fragment", "https://model.example/v1?", "https://model.example/v1#", "not a URL"]) {
    await set.getByLabel("Base URL").fill(url);
    await expect(set.getByLabel("Base URL")).toHaveAttribute("aria-invalid", "true");
    await expect(set.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
    await set.getByLabel("API key", { exact: true }).press("Enter");
  }
  expect(await writes(request)).toEqual([]);
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await expect(set.getByLabel("Harness configuration (JSON)")).toBeHidden();
  await set.getByText("Advanced settings", { exact: true }).click();
  const config = set.getByLabel("Harness configuration (JSON)");
  await config.fill("[]");
  await config.blur();
  await expect(set.getByText("Enter a valid JSON object, such as {}.")).toBeVisible();
  await expect(set.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await config.fill('{"model_reasoning_effort":"high"}');
  await set.getByRole("button", { name: "Format JSON" }).click();
  await expect(config).toHaveValue('{\n  "model_reasoning_effort": "high"\n}');
  // Limits are Core's 32-bit whole numbers, and max output needs a context window at least as large.
  await set.getByLabel("Max output tokens").fill("32000");
  await expect(set.getByText("Set a context window at least this large.")).toBeVisible();
  await expect(set.getByLabel("Context window", { exact: true })).toHaveAttribute("aria-invalid", "true");
  await expect(set.getByLabel("Context window", { exact: true })).toHaveAccessibleDescription(/Set a context window at least this large/);
  await expect(set.getByLabel("Max output tokens", { exact: true })).not.toHaveAttribute("aria-invalid", "true");
  await set.getByLabel("Context window", { exact: true }).fill("1000");
  await expect(set.getByLabel("Context window", { exact: true })).toHaveAttribute("aria-invalid", "true");
  await set.getByLabel("Context window").fill("2147483648");
  await expect(set.getByText("Enter a whole number up to 2147483647.")).toBeVisible();
  await expect(set.getByRole("button", { name: "Save" })).toBeDisabled();
  await set.getByLabel("Context window").fill("200000");
  await set.getByRole("button", { name: "Save" }).click();
  await expect(set).toBeHidden();
  for (const text of ["OpenAI Responses", "https://model.example/v1", "Configured", "200,000", "32,000"]) await expect(codex).toContainText(text);
  expect(await page.content()).not.toContain(KEY);

  // Replacing starts from the saved fields but never from the key; closing the form forgets a typed key.
  await codex.getByRole("button", { name: "Replace the default model configuration for Codex" }).click();
  let replace = page.getByRole("dialog", { name: "Replace default model configuration for Codex" });
  await expect(replace.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText("OpenAI Responses");
  await expect(replace.getByLabel("Default model ID")).toHaveValue("fixture-model");
  await expect(replace.getByLabel("Harness configuration (JSON)")).toHaveValue(/model_reasoning_effort/);
  await expect(replace.getByLabel("Base URL")).toHaveValue("https://model.example/v1");
  await expect(replace.getByLabel("API key")).toHaveValue("");
  await expect(replace.getByRole("button", { name: "Save" })).toBeDisabled();
  await replace.getByLabel("API key").fill(KEY);
  const nativeSettings = replace.getByLabel("Harness configuration (JSON)");
  await expect(nativeSettings).toHaveValue(/model_reasoning_effort/);
  await replace.getByLabel("Default model ID").fill("different-model");
  await expect(nativeSettings).toHaveValue("{}");
  await nativeSettings.fill('{"model_reasoning_effort":"low"}');
  await replace.getByLabel("Base URL").fill("https://another-model.example/v1");
  await expect(nativeSettings).toHaveValue("{}");
  await nativeSettings.fill('{"model_reasoning_effort":"high"}');
  await replace.getByRole("button", { name: "Cancel" }).click();
  expect(await page.content()).not.toContain(KEY);
  await codex.getByRole("button", { name: "Replace the default model configuration for Codex" }).click();
  replace = page.getByRole("dialog", { name: "Replace default model configuration for Codex" });
  await expect(replace.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText("OpenAI Responses");
  await expect(replace.getByLabel("Default model ID")).toHaveValue("fixture-model");
  await expect(replace.getByLabel("Harness configuration (JSON)")).toHaveValue(/model_reasoning_effort/);
  await expect(replace.getByLabel("API key")).toHaveValue("");
  await replace.getByLabel("Base URL").fill("https://model.example/v2");
  await replace.getByLabel("API key").fill(KEY);
  await expect(replace.getByLabel("Harness configuration (JSON)")).toHaveValue("{}");
  // Enter saves.
  await replace.getByLabel("API key").press("Enter");
  await expect(replace).toBeHidden();
  await expect(codex).toContainText("https://model.example/v2");
  await expect(codex).toContainText("OpenAI Responses");

  await codex.getByRole("button", { name: "Clear the default model configuration for Codex" }).click();
  const confirm = page.getByRole("dialog", { name: "Clear default model configuration" });
  await confirm.getByRole("button", { name: "Clear default model configuration" }).click();
  await expect(confirm).toBeHidden();
  await expect(codex).toContainText("Not set");

  // MiniMax Code needs both limits; the form shows Core's reason. A disabled harness may still be configured.
  await mcode.getByRole("button", { name: "Set the default model configuration for MiniMax Code" }).click();
  const limits = page.getByRole("dialog", { name: "Set default model configuration for MiniMax Code" });
  await expect(limits.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText("Anthropic Messages");
  await limits.getByRole("combobox", { name: "Protocol", exact: true }).click();
  await expect(page.getByRole("option")).toHaveText(["Anthropic Messages", "OpenAI Responses", "OpenAI Chat Completions"]);
  await page.getByRole("option", { name: "OpenAI Responses", exact: true }).click();
  await limits.getByLabel("Base URL").fill("https://model.example/anthropic");
  await limits.getByLabel("API key").fill(KEY);
  await limits.getByLabel("Default model ID").fill("fixture-model");
  await expect(limits.getByRole("button", { name: "Save" })).toBeDisabled();
  await limits.getByRole("button", { name: "Cancel" }).click();
  await expect(mcode).toContainText("Not set");

  expect(await writes(request)).toEqual([
    "PUT /core/v1/harnesses/codex/model-configuration",
    "PUT /core/v1/harnesses/codex/model-configuration",
    "DELETE /core/v1/harnesses/codex/model-configuration",
  ]);
  const browserState = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, url: location.href, cookie: document.cookie }));
  expect(browserState).not.toContain(KEY);
});

test("reports an unconfirmed save, reads the default model configurations again once and never repeats the write", async ({ page, request }) => {
  await openConsole(page, request, "system", { fresh: true });
  const codex = page.getByRole("region", { name: "Default model configuration" }).getByRole("article", { name: "Codex" });
  await codex.getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const set = page.getByRole("dialog", { name: "Set default model configuration for Codex" });
  await set.getByLabel("Base URL").fill("https://model.example/v1");
  await set.getByLabel("API key").fill(KEY);
  await set.getByLabel("Default model ID").fill("fixture-model");
  await failNext(request, { method: "PUT", path: "/harnesses/codex/model-configuration", status: 500 });
  const reads: string[] = [];
  page.on("request", (sent) => { if (sent.method() === "GET" && new URL(sent.url()).pathname === "/core/v1/harnesses") reads.push(sent.url()); });
  await set.getByRole("button", { name: "Save" }).click();
  await expect(set.getByRole("alert")).toHaveText("Core did not confirm the change. The default model configurations were read again; check them before trying again.");
  await expect.poll(() => reads.length).toBe(1);
  await page.waitForTimeout(500);
  expect(reads).toHaveLength(1);
  expect(await writes(request)).toEqual(["PUT /core/v1/harnesses/codex/model-configuration"]);
});

test("requires an explicit supported protocol choice for an older default configuration", async ({ page, request }) => {
  await page.route("**/core/v1/harnesses", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    const codex = data.data.find((harness: { id: string }) => harness.id === "codex");
    codex.model_configuration.model_provider.protocol = "chat_completions";
    codex.model_configuration.harness_config = { model_reasoning_effort: "high" };
    await route.fulfill({ response, json: data });
  });
  await openConsole(page, request, "system");
  const codex = page.getByRole("region", { name: "Default model configuration" }).getByRole("article", { name: "Codex" });
  await expect(codex).toContainText("OpenAI Chat Completions");
  await codex.getByRole("button", { name: "Replace the default model configuration for Codex" }).click();
  const dialog = page.getByRole("dialog", { name: "Replace default model configuration for Codex" });
  await dialog.getByLabel("API key").fill(KEY);
  await expect(dialog.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText("Select protocol");
  await expect(dialog.getByRole("alert")).toHaveText("This harness does not support OpenAI Chat Completions. Select a supported protocol.");
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await dialog.getByLabel("API key").press("Enter");
  expect(await writes(request)).toEqual([]);
  await page.screenshot({ path: test.info().outputPath("unsupported-saved-protocol.png"), animations: "disabled" });
  await dialog.getByRole("combobox", { name: "Protocol", exact: true }).click();
  await expect(page.getByRole("option")).toHaveText(["OpenAI Responses"]);
  await page.getByRole("option", { name: "OpenAI Responses", exact: true }).click();
  await expect(dialog.getByRole("alert")).toHaveCount(0);
  await expect(dialog.getByLabel("Harness configuration (JSON)")).toHaveValue("{}");
  await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([]);
});
