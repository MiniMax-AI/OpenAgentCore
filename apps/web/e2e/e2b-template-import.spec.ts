import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { expectManagementBoundary, openConsole, writes } from "./console";

const fixture = JSON.parse(readFileSync(new URL("../../../services/core/deploy/e2b/testdata/template-manifests.json", import.meta.url), "utf8"))[0].value;
const build = { ...fixture, api_url: "https://sandbox.sandbase.ai", domain: "sandbox.sandbase.ai", template: "template:94be54a1-138c-4f30-bc87-b13686272dbe" };
const discoveryPath = "/core/v1/sandbox/providers/e2b/discovery";
async function upload(page: Page, value: unknown) {
  await page.getByLabel("Import template build", { exact: true }).setInputFiles({ name: "build.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(value)) });
}

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("imports a build locally, forgets the old key and saves only validated configuration fields", async ({ page, request }, info) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud", exact: true }).click();
  await page.getByLabel("E2B API key", { exact: true }).fill("old-fixture-key");
  await upload(page, build);
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("Sandbox API URL", { exact: true })).toHaveValue(build.api_url);
  await expect(page.getByLabel("Template build", { exact: true })).toHaveValue(build.template);
  await expect(page.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
  // Give a pending discovery debounce time to fire; import must cancel it.
  await page.waitForTimeout(650);
  expect(await writes(request)).toEqual([]);
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-key");
  await expect(page.getByLabel("Template build", { exact: true })).toHaveValue(build.template);
  await expect(page.getByRole("button", { name: "Next", exact: true })).toBeEnabled();
  await page.screenshot({ path: info.outputPath("import-en.png"), fullPage: true });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  const sent = page.waitForRequest((r) => r.method() === "POST" && r.url().endsWith("/sandbox/deployment"));
  await page.getByRole("button", { name: "Save configuration", exact: true }).click();
  expect((await sent).postDataJSON()).toEqual({ provider: "e2b", expected_generation: 0, configuration: { api_url: build.api_url, domain: build.domain, template: build.template }, credential: { api_key: "fixture-private-key" } });
  await expect(page.getByRole("heading", { name: "Sandbox configuration", level: 1 })).toBeVisible();
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(storage).not.toContain("fixture-private-key");
  expect(storage).not.toContain(build.template);
});

test("rejects credentials in a file and clears entered credentials when changing endpoints", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud", exact: true }).click();
  await upload(page, { ...build, api_key: "must-not-be-imported" });
  await expect(page.getByRole("alert")).toContainText("valid OpenAgentCore template build JSON");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  expect(await writes(request)).toEqual([]);
  await upload(page, build);
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-key");
  await page.getByLabel("Sandbox API URL", { exact: true }).fill("https://custom.sandbase.ai");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("Template build", { exact: true })).toHaveValue("");
  await expect(page.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
});

test("offers build guidance for an empty catalog and identifies both discovery failure stages", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud", exact: true }).click();
  let mode = "empty";
  await page.route(`**${discoveryPath}`, async (route) => {
    const query = route.request().postDataJSON().query;
    if (mode === "empty") return route.fulfill({ json: { templates: [] } });
    if (mode === "templates-error" || query.template) return route.fulfill({ status: 503, json: { error: { message: "fixture", code: "sandbox_verification_unconfirmed" } } });
    return route.fulfill({ json: { templates: [{ id: "template", names: ["fixture-runtime"] }] } });
  });
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-key");
  await expect(page.getByText("No templates are visible to this key.", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Build an OpenAgentCore template", exact: true })).toHaveAttribute("href", /deploy\/e2b\/README.md#build-a-template$/);
  mode = "templates-error";
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-second-key");
  await expect(page.getByRole("alert")).toContainText("Template discovery failed");
  mode = "builds-error";
  await page.getByRole("button", { name: "Try again", exact: true }).click();
  await page.getByLabel("Template", { exact: true }).selectOption("template");
  await expect(page.getByRole("alert")).toContainText("Build discovery failed");
  await page.getByRole("button", { name: "Enter an exact template build", exact: true }).click();
  await page.getByLabel("Template build", { exact: true }).fill("template:latest");
  await expect(page.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
  await page.getByLabel("Template build", { exact: true }).fill(build.template);
  await expect(page.getByRole("button", { name: "Next", exact: true })).toBeEnabled();
});

test("a delayed import cannot overwrite a later endpoint edit", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud", exact: true }).click();
  await page.evaluate(() => {
    const original = File.prototype.text;
    File.prototype.text = function () {
      return new Promise<string>((resolve, reject) => {
        (window as unknown as { finishBuildImport: () => void }).finishBuildImport = () => { void original.call(this).then(resolve, reject); };
      });
    };
  });
  await upload(page, build);
  await page.getByLabel("Sandbox API URL", { exact: true }).fill("https://custom.sandbase.ai");
  await page.evaluate(() => (window as unknown as { finishBuildImport: () => void }).finishBuildImport());
  await expect(page.getByLabel("Sandbox API URL", { exact: true })).toHaveValue("https://custom.sandbase.ai");
  await expect(page.getByText("Build imported.", { exact: false })).toHaveCount(0);
  expect(await writes(request)).toEqual([]);
});

test("an imported build still surfaces Core admission rejection and supports Chinese", async ({ page, request }, info) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "none" });
  await page.getByRole("button", { name: "E2B cloud", exact: true }).click();
  await upload(page, build);
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-key");
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    return route.fulfill({ status: 400, json: { error: { message: "fixture", code: "sandbox_configuration_invalid", param: "configuration" } } });
  });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.getByRole("button", { name: "Save configuration", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Connect E2B", exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Select a ready immutable E2B template build with matching resources.");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("Template build", { exact: true })).toHaveValue(build.template);
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  await expect(page.getByLabel("导入模板构建文件", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "构建 OpenAgentCore 模板", exact: true })).toBeVisible();
  await page.screenshot({ path: info.outputPath("import-zh-rejected.png"), fullPage: true });
});
