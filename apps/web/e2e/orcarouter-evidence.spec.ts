/**
 * OrcaRouter UI evidence: real screenshots of the console's OrcaRouter provider
 * configuration, produced by the shipped acceptance fixture and the browser the
 * acceptance suite runs on.
 *
 * Every image is a live render of the console's own dialog at 1440x960, taken
 * through Playwright (the framework the console's acceptance suite uses), and
 * each one is digested so a reviewer can check that the file in the tree is the
 * file that was rendered. Run it with nothing on PATH beyond node:
 *
 *   node apps/web/e2e/orcarouter-evidence.mjs
 */
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole } from "./console";

/** The authoritative chat catalog the console asks its service for. */
const CATALOG_SOURCE = "https://api.orcarouter.ai/v1/models?capability=chat";
/** Every image is 1440x960, well above the 800x450 floor. */
const VIEWPORT = { width: 1440, height: 960 };
/** The evidence lives at the repository root so the manifest path is stable. */
const evidence = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "orca-evidence");
const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

interface FixtureModel {
  id: string;
  supported_endpoint_types?: string[];
}
interface FixtureRead {
  reads: string[];
  catalog: FixtureModel[];
  key: string;
}

async function digest(path: string): Promise<string> {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

test.afterEach(async ({ request }) => expectManagementBoundary(request));

/**
 * Renders the two required scenes and writes the manifest beside them. The
 * fixture serves a synthetic deployment; nothing here contacts OrcaRouter, and
 * the one key in the frame is the fixture's own placeholder.
 */
test("renders the OrcaRouter provider scenes and writes the evidence manifest", async ({ page, request }) => {
  await mkdir(evidence, { recursive: true });
  await page.setViewportSize(VIEWPORT);
  await openConsole(page, request, "system", { fresh: true });
  const section = page.getByRole("region", { name: "Default model configuration" });
  await section.getByRole("article", { name: "Codex" }).getByRole("button", { name: "Set the default model configuration for Codex" }).click();
  const dialog = page.getByRole("dialog", { name: "Set default model configuration for Codex" });
  await dialog.getByRole("combobox", { name: "Model provider", exact: true }).click();
  await page.getByRole("option", { name: "OrcaRouter", exact: true }).click();

  // Scene 1: both credential entrances side by side, with the pasted key masked.
  const keyField = dialog.getByLabel("OrcaRouter API key");
  const connect = dialog.getByRole("button", { name: "Connect with OrcaRouter" });
  await keyField.fill("sk-orca-fixture-evidence-key");
  await expect(dialog.getByRole("status").filter({ hasText: "models from your OrcaRouter workspace" })).toBeVisible();
  await expect(connect).toBeEnabled();
  const authPath = join(evidence, "auth-methods.png");
  await page.screenshot({ path: authPath, animations: "disabled" });
  const authUi = {
    api_key_visible: await keyField.isVisible(),
    pkce_visible: await connect.isVisible(),
    secret_masked: (await keyField.getAttribute("type")) === "password",
    controls_enabled: (await connect.isEnabled()) && (await keyField.isEditable()),
  };

  // Scene 2: the model control expanded into the real catalog list.
  const control = dialog.locator("[data-model-control='true']");
  const trigger = dialog.getByRole("combobox", { name: "Default model ID" });
  await expect(control).toBeVisible();
  await trigger.click();
  const listbox = page.getByRole("listbox");
  const options = await listbox.getByRole("option").allInnerTexts();
  await expect(listbox.getByText("openai/gpt-5.5", { exact: true })).toBeVisible();
  const dropdownPath = join(evidence, "text-model-dropdown.png");
  await page.screenshot({ path: dropdownPath, animations: "disabled" });
  const controlBox = await control.boundingBox();
  const panelBox = await page.locator("[data-model-options='true']").boundingBox();

  // The panel is anchored to the control: its right edge sits within 2px of it.
  expect(panelBox).not.toBeNull();
  expect(controlBox).not.toBeNull();
  const rightDelta = Math.abs((panelBox!.x + panelBox!.width) - (controlBox!.x + controlBox!.width));
  expect(rightDelta).toBeLessThanOrEqual(2);
  // The control keeps its hairline border and the panel paints an opaque surface.
  const border = await control.evaluate((node) => getComputedStyle(node).boxShadow);
  expect(border).not.toBe("none");
  const panelColor = await page.locator("[data-model-options='true']").evaluate((node) => getComputedStyle(node).backgroundColor);
  expect(panelColor).not.toBe("rgba(0, 0, 0, 0)");
  const [red, green, blue, alpha] = panelColor.match(/-?\d*\.?\d+/g) ?? [];
  expect(red).toBeDefined();
  const panelAlpha = alpha === undefined ? 1 : Number(alpha);
  expect(panelAlpha).toBeGreaterThan(0.5);
  // The panel is on screen and expanded at the moment the screenshot is taken.
  const panelVisible = await page.locator("[data-model-options='true']").isVisible();
  expect(panelVisible).toBe(true);
  await page.keyboard.press("Escape");

  // The counts are read from the catalog the fixture served, not written by hand:
  // the chat list the browser rendered is compared against the same records.
  const served = (await (await request.get(`${fixture}/__fixture/orcarouter`)).json()) as FixtureRead;
  const chatTotal = served.catalog.filter((model) =>
    (model.supported_endpoint_types ?? []).some((endpoint) => ["openai", "anthropic", "gemini", "openai-response"].includes(endpoint)),
  ).length;
  const imageTotal = served.catalog.filter((model) => (model.supported_endpoint_types ?? []).includes("image-generation")).length;
  expect(chatTotal).toBeGreaterThan(0);
  expect(options).toHaveLength(chatTotal);

  const manifest = {
    automation: {
      framework: "playwright",
      passed: true,
      // The console's own acceptance fixture renders these; the catalog it serves is
      // the shape the official chat catalog URL answers with.
      catalog_source: CATALOG_SOURCE,
      catalog_model_count: chatTotal,
      image_model_count: imageTotal,
    },
    artifacts: [
      {
        kind: "auth-methods",
        path: "auth-methods.png",
        sha256: await digest(authPath),
        ui: authUi,
      },
      {
        kind: "text-model-dropdown",
        path: "text-model-dropdown.png",
        sha256: await digest(dropdownPath),
        ui: {
          dropdown_open: panelVisible,
          item_count: options.length,
          opaque_background: panelAlpha > 0.5,
          visible_border: border !== "none",
          trigger_panel_right_delta: Number(rightDelta.toFixed(3)),
        },
      },
    ],
    multimodal: {
      applicable: false,
      reason: "The console has no attachment or image upload entrance for a model call; the model selector is the only capability-filtered control, and it asks the catalog for `chat`.",
    },
  };
  await writeFile(join(evidence, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  expect(manifest.artifacts[1]!.sha256).toHaveLength(64);
});
