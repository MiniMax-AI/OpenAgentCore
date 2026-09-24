import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

// Resources › Files against the fixture Core: the list, upload, ordering,
// cursor pages, the loaded-only filter and confirmed deletion. Files never
// carry the Agents Beta header and user_data content is never downloaded.

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

interface RecordedRequest {
  method: string;
  path: string;
  query: string;
  beta: string | null;
  body?: Record<string, unknown>;
}

interface SourceFile {
  id: string;
  filename: string;
  bytes: number;
}

async function control(request: APIRequestContext, values: Record<string, unknown>) {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/control`, { data: values })).ok()).toBe(true);
}

async function requests(request: APIRequestContext): Promise<RecordedRequest[]> {
  return (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json();
}

async function seed(request: APIRequestContext, filename: string, content: string): Promise<SourceFile> {
  const response = await request.post(`${fixtureBaseUrl}/v1/files`, {
    multipart: { purpose: "user_data", file: { name: filename, mimeType: "text/plain", buffer: Buffer.from(content) } },
  });
  expect(response.status()).toBe(200);
  return response.json();
}

function consoleNav(page: Page) {
  return page.getByRole("navigation", { name: "Console navigation", exact: true });
}

function filesPage(page: Page) {
  return page.locator(".files-page");
}

function table(page: Page) {
  return filesPage(page).getByRole("table", { name: "Files", exact: true });
}

async function openFiles(page: Page) {
  await page.goto("/#files");
  await expect(filesPage(page).getByRole("heading", { name: "Files", exact: true })).toBeVisible();
}

async function chooseFile(page: Page, name: string, content: string) {
  await filesPage(page).locator('input[type="file"]').setInputFiles({ name, mimeType: "text/plain", buffer: Buffer.from(content) });
}

test.beforeEach(async ({ request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
});

test.afterEach(async ({ request }) => {
  const files = (await requests(request)).filter((entry) => entry.path.startsWith("/v1/files"));
  expect(files.every((entry) => entry.beta === null)).toBe(true);
  expect(files.filter((entry) => entry.path.endsWith("/content"))).toEqual([]);
});

test("opens Files from the Resources navigation with an explicit empty list", async ({ page }) => {
  await page.goto("/");
  await consoleNav(page).getByRole("button", { name: "Files", exact: true }).click();
  await expect(page).toHaveURL(/#files$/u);
  await expect(filesPage(page).getByText("No files yet", { exact: true })).toBeVisible();
  await expect(filesPage(page).getByRole("button", { name: "Upload file", exact: true })).toBeEnabled();
  await expect(filesPage(page)).toContainText("User data files cannot be downloaded");
  await expect(filesPage(page).getByRole("button", { name: /download/iu })).toHaveCount(0);
});

test("uploads a file, shows it in the list, then deletes it after confirmation", async ({ page, request }) => {
  await openFiles(page);
  await chooseFile(page, "notes.txt", "abc");
  const row = table(page).getByRole("row").filter({ hasText: "notes.txt" });
  await expect(row).toBeVisible();
  await expect(row).toHaveClass(/files-row-new/u);
  await expect(row).toContainText("3 B");
  await expect(row).toContainText("user_data");

  const uploads = (await requests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/files");
  expect(uploads).toHaveLength(1);
  expect(uploads[0]!.body).toEqual({ filename: "notes.txt", bytes: 3, purpose: "user_data" });
  const reads = (await requests(request)).filter((entry) => entry.method === "GET" && entry.path === "/v1/files");
  expect(reads.length).toBeGreaterThanOrEqual(2);
  expect(new URLSearchParams(reads.at(-1)!.query).get("limit")).toBe("100");

  const listed = await (await request.get(`${fixtureBaseUrl}/v1/files`)).json();
  const id = listed.data[0].id as string;
  await expect(row.getByRole("button", { name: "Copy File ID", exact: true })).toBeVisible();
  await expect(row.locator(`code[title="${id}"]`)).toBeVisible();

  await row.getByRole("button", { name: "Delete notes.txt", exact: true }).click();
  const confirmation = filesPage(page).getByRole("group", { name: "Delete notes.txt?", exact: true });
  await expect(confirmation).toContainText("Copies already placed in Session workspaces are not affected");
  await expect(confirmation).toContainText("neither are existing Sessions");
  await expect(confirmation).toContainText("Environment Template that references this File ID will fail");
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  expect((await requests(request)).filter((entry) => entry.method === "DELETE")).toEqual([]);

  await row.getByRole("button", { name: "Delete notes.txt", exact: true }).click();
  await filesPage(page).getByRole("group", { name: "Delete notes.txt?", exact: true }).getByRole("button", { name: "Delete file", exact: true }).click();
  await expect(filesPage(page).getByText("No files yet", { exact: true })).toBeVisible();
  const deletions = (await requests(request)).filter((entry) => entry.method === "DELETE");
  expect(deletions.map((entry) => entry.path)).toEqual([`/v1/files/${id}`]);
  expect((await request.get(`${fixtureBaseUrl}/v1/files/${id}`)).status()).toBe(404);
});

test("lists files uploaded through the API, pages with a cursor and filters loaded rows only", async ({ page, request }) => {
  const first = await seed(request, "alpha.csv", "a,b\n1,2\n");
  await seed(request, "beta.json", "{}");
  const third = await seed(request, "gamma.txt", "");
  await control(request, { sourceListPageSize: 2 });
  await openFiles(page);

  await expect(table(page).getByRole("row").nth(1)).toContainText("gamma.txt");
  await expect(table(page)).toContainText("0 B");
  await expect(table(page)).not.toContainText("alpha.csv");
  await expect(filesPage(page)).toContainText("2 loaded");

  const search = filesPage(page).getByRole("searchbox", { name: "Filter loaded files", exact: true });
  await expect(search).toHaveAttribute("placeholder", "Filter loaded files by name or ID");
  await search.fill("alpha");
  await expect(filesPage(page).getByText("No loaded files match", { exact: true })).toBeVisible();

  await filesPage(page).getByRole("button", { name: "Load more", exact: true }).click();
  await expect(table(page).getByRole("row").filter({ hasText: "alpha.csv" })).toBeVisible();
  await expect(filesPage(page)).toContainText("1 of 3 loaded");
  await expect(filesPage(page).getByRole("button", { name: "Load more", exact: true })).toHaveCount(0);
  const reads = (await requests(request)).filter((entry) => entry.method === "GET" && entry.path === "/v1/files");
  expect(new URLSearchParams(reads.at(-1)!.query).get("after")).toBe(
    (await (await request.get(`${fixtureBaseUrl}/v1/files?limit=2`)).json()).last_id,
  );

  await search.fill("");
  await filesPage(page).getByRole("radio", { name: "Oldest first", exact: true }).click();
  await expect(table(page).getByRole("row").nth(1)).toContainText(first.filename);
  const ordered = (await requests(request)).filter((entry) => entry.method === "GET" && entry.path === "/v1/files").at(-1)!;
  expect(new URLSearchParams(ordered.query).get("order")).toBe("asc");
  expect(third.bytes).toBe(0);
});

test("never retries an upload whose response was lost and asks for a refresh instead", async ({ page, request }) => {
  await openFiles(page);
  await control(request, { sourceUploadResponseLoss: 1 });
  await chooseFile(page, "report.pdf", "%PDF-1.7");
  const alert = filesPage(page).getByRole("alert").filter({ hasText: "may have reached Core" });
  await expect(alert).toContainText("Refresh the list to check before uploading it again.");
  expect((await requests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/files")).toHaveLength(1);
  await alert.getByRole("button", { name: "Refresh files", exact: true }).click();
  await expect(table(page).getByRole("row").filter({ hasText: "report.pdf" })).toBeVisible();
  expect((await requests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/files")).toHaveLength(1);
});

test("shows a definite upload rejection and storage without configuration", async ({ page, request }) => {
  await openFiles(page);
  await control(request, { sourceUploadStatus: 400 });
  await chooseFile(page, "bad.txt", "x");
  await expect(filesPage(page).getByRole("alert")).toContainText("Core rejected the upload of bad.txt");
  await filesPage(page).getByRole("button", { name: "Dismiss", exact: true }).click();

  await control(request, { sourceListStatus: 503 });
  await filesPage(page).getByRole("button", { name: "Refresh files", exact: true }).click();
  await expect(filesPage(page).getByText("File storage is not configured", { exact: true })).toBeVisible();
  await expect(filesPage(page).getByRole("button", { name: "Upload file", exact: true })).toBeDisabled();
});

test("shows Chinese copy with API terms kept in English", async ({ page, request }) => {
  await seed(request, "notes.txt", "abc");
  await page.addInitScript(() => localStorage.setItem("agents-core-web.language", "zh-CN"));
  await openFilesInChinese(page);
  await expect(filesPage(page).getByRole("searchbox")).toHaveAttribute("placeholder", "按名称或 ID 筛选已加载的文件");
  await expect(filesPage(page).getByRole("button", { name: "复制 File ID", exact: true })).toBeVisible();
});

async function openFilesInChinese(page: Page) {
  await page.goto("/#files");
  await expect(filesPage(page).getByRole("heading", { name: "文件", exact: true })).toBeVisible();
}
