import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";

// Skills lifecycle against the fixture Core (apps/web/e2e/fixture-skills.mjs):
// navigation visibility, ZIP and folder uploads, the default pointer,
// downloads and the version deletion rules.

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

interface SkillCall {
  method: string;
  path: string;
  beta: string | null;
  multipart?: { fields: string[]; filenames: string[]; defaults: string[] } | "invalid";
  body?: unknown;
}

const firstManifest = "---\nname: report\ndescription: Create the report.\n---\nFollow the report procedure.\n";
const secondManifest = "---\nname: report\ndescription: Create the quarterly report.\n---\nFollow the quarterly procedure.\n";

/** A stored (uncompressed) ZIP; the fixture Core and the browser preview both read it. */
function storedZip(entries: Array<[path: string, content: string]>): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const [path, content] of entries) {
    const name = Buffer.from(path, "utf8");
    const data = Buffer.from(content, "utf8");
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x800, 6);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0x800, 8);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, name, data);
    centrals.push(central, name);
    offset += local.length + name.length + data.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

async function resetFixture(request: APIRequestContext): Promise<void> {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
}

async function skillsFixture(request: APIRequestContext): Promise<{ calls: SkillCall[]; skills: unknown[] }> {
  const response = await request.get(`${fixtureBaseUrl}/__fixture/skills`);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<{ calls: SkillCall[]; skills: unknown[] }>;
}

function consoleNav(page: Page): Locator {
  return page.getByRole("navigation", { name: "Console navigation", exact: true });
}

async function openSkills(page: Page): Promise<void> {
  const entry = consoleNav(page).getByRole("button", { name: "Skills", exact: true });
  await entry.click();
  await expect(entry).toHaveAttribute("aria-current", "page");
}

async function uploadZipSkill(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Upload Skill" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Upload Skill" });
  await dialog.locator('input[type="file"]').setInputFiles({
    name: "report.zip",
    mimeType: "application/zip",
    buffer: storedZip([["report/SKILL.md", firstManifest], ["report/notes.txt", "notes"]]),
  });
  // The preview reads SKILL.md frontmatter as plain text.
  const preview = dialog.getByRole("group", { name: "Selection" });
  await expect(preview).toContainText("Create the report.");
  await expect(preview).toContainText("report");
  await dialog.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(dialog).toBeHidden();
}

test.describe("Skills navigation", () => {
  for (const status of [404, 405]) {
    test(`hides Skills when the connected Core answers ${status}`, async ({ page, request }) => {
      await resetFixture(request);
      await page.route("**/v1/skills?*", async (route) => {
        await route.fulfill({ status, json: { error: { message: "Unavailable.", type: "invalid_request_error", code: null, param: null } } });
      });
      await page.goto("/");
      await expect(consoleNav(page).getByRole("button", { name: "Overview", exact: true })).toBeVisible();
      await expect(consoleNav(page).getByRole("button", { name: "Skills", exact: true })).toHaveCount(0);
    });
  }

  test("shows Skills with an explanation when Core has no Skill storage", async ({ page, request }) => {
    await resetFixture(request);
    expect((await request.post(`${fixtureBaseUrl}/__fixture/skills?availability=storage-unavailable`)).ok()).toBe(true);
    await page.goto("/");
    await openSkills(page);
    await expect(page.getByText("Core has no Skill storage configured")).toBeVisible();
    await expect(page.getByText("Skills could not be loaded")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Upload Skill" })).toBeDisabled();
  });
});

test("uploads a ZIP and a folder version, moves the default, downloads and deletes every version", async ({ page, request }) => {
  await resetFixture(request);
  const folderRoot = mkdtempSync(join(tmpdir(), "parsar-skills-e2e-"));
  mkdirSync(join(folderRoot, "report"));
  writeFileSync(join(folderRoot, "report", "SKILL.md"), secondManifest);
  writeFileSync(join(folderRoot, "report", ".DS_Store"), "finder metadata");
  try {
    await page.goto("/");
    await openSkills(page);
    await expect(page.getByText("No Skills yet")).toBeVisible();

    // 1. Upload a ZIP; the new Skill opens.
    await uploadZipSkill(page);
    await expect(page.getByRole("heading", { level: 1, name: /report$/ })).toBeVisible();
    const facts = page.getByLabel("Skill details", { exact: true });
    const versions = page.getByRole("region", { name: "Versions", exact: true });
    await expect(facts).toContainText("Create the report.");
    await expect(versions.getByRole("row", { name: /\bv1\b/ })).toBeVisible();

    // 2. Upload a folder as v2 without making it the default.
    await page.getByRole("button", { name: "Upload new version" }).click();
    const versionDialog = page.getByRole("dialog", { name: "Upload a new version of report" });
    await versionDialog.getByRole("radio", { name: "Folder" }).click();
    await versionDialog.locator('input[type="file"]').setInputFiles(join(folderRoot, "report"));
    // Hidden files are listed, never filtered out.
    await expect(versionDialog.getByText("Hidden files are uploaded as they are: report/.DS_Store")).toBeVisible();
    await expect(versionDialog.getByRole("checkbox", { name: "Make this the default version" })).not.toBeChecked();
    await versionDialog.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(versionDialog).toBeHidden();
    await expect(versions.getByRole("row", { name: /\bv2\b/ })).toBeVisible();
    await expect(facts).toContainText("Newer than default");
    await expect(facts).toContainText("Create the report.");

    const { calls } = await skillsFixture(request);
    expect(calls.length).toBeGreaterThan(0);
    expect(calls.every((call) => call.beta === null)).toBe(true);
    const zipUpload = calls.find((call) => call.method === "POST" && call.path === "/v1/skills");
    expect(zipUpload?.multipart).toEqual({ fields: ["files"], filenames: ["report.zip"], defaults: [] });
    const folderUpload = calls.find((call) => call.method === "POST" && call.path.endsWith("/versions"));
    expect(folderUpload?.multipart).not.toBe("invalid");
    const folderForm = folderUpload?.multipart as { fields: string[]; filenames: string[]; defaults: string[] };
    expect(folderForm.fields).toEqual(["files[]", "files[]"]);
    expect([...folderForm.filenames].sort()).toEqual(["report/.DS_Store", "report/SKILL.md"]);
    expect(folderForm.defaults).toEqual([]);

    // 3. Make v2 the default; the Skill takes v2's description.
    await versions.getByRole("button", { name: "Make v2 the default" }).click();
    const confirm = page.getByRole("dialog", { name: "Make v2 the default?" });
    await expect(confirm).toContainText("Existing Sessions are not affected.");
    await confirm.getByRole("button", { name: "Make default" }).click();
    await expect(confirm).toBeHidden();
    await expect(facts).toContainText("Create the quarterly report.");
    await expect(facts).not.toContainText("Newer than default");

    // 4. Download v1 through the client.
    const downloadEvent = page.waitForEvent("download");
    await versions.getByRole("button", { name: "Download v1", exact: true }).click();
    const download = await downloadEvent;
    expect(download.suggestedFilename()).toBe("report-v1.zip");
    expect((await skillsFixture(request)).calls.some((call) => call.method === "GET" && /\/versions\/1\/content$/.test(call.path))).toBe(true);

    // 5. Delete v1. The default stays undeletable while v1 remains.
    await expect(versions.getByRole("button", { name: /^Delete v2\b/ })).toBeDisabled();
    await versions.getByRole("button", { name: "Delete v1", exact: true }).click();
    const deleteVersion = page.getByRole("dialog", { name: "Delete v1?" });
    await expect(deleteVersion).toContainText("Version numbers are never reused.");
    await deleteVersion.getByRole("button", { name: "Delete version", exact: true }).click();
    await expect(deleteVersion).toBeHidden();
    await expect(versions.getByRole("row", { name: /\bv1\b/ })).toHaveCount(0);

    // 6. Delete the only remaining version, which deletes the Skill.
    await versions.getByRole("button", { name: "Delete v2", exact: true }).click();
    const deleteLast = page.getByRole("dialog", { name: "Delete the only version?" });
    await expect(deleteLast).toContainText("This is the only version. Deleting it deletes the whole Skill.");
    await deleteLast.getByRole("button", { name: "Delete version and Skill" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Skills", exact: true })).toBeVisible();
    await expect(page.getByText("No Skills yet")).toBeVisible();
    expect((await skillsFixture(request)).skills).toEqual([]);
  } finally {
    rmSync(folderRoot, { recursive: true, force: true });
  }
});

test("deletes a Skill only after its name is typed", async ({ page, request }) => {
  await resetFixture(request);
  await page.goto("/");
  await openSkills(page);
  await uploadZipSkill(page);

  await page.getByRole("button", { name: "Delete Skill" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete report?" });
  await expect(dialog).toContainText("Existing Sessions are not affected.");
  await expect(dialog).toContainText("Environment templates that reference this Skill will fail");
  const confirm = dialog.getByRole("button", { name: "Delete Skill" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Type report to confirm").fill("repor");
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Type report to confirm").fill("report");
  await confirm.click();
  await expect(page.getByText("No Skills yet")).toBeVisible();
  expect((await skillsFixture(request)).skills).toEqual([]);
});
