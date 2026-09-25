import { expect, type APIRequestContext, type BrowserContext, type Page } from "@playwright/test";

export const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18611}`;
export const web = `http://127.0.0.1:${process.env.AGENTS_WEB_PORT ?? 19619}`;
const browserRequests = new WeakMap<Page, Array<{ path: string; authorization: string | undefined }>>();
export function observeBrowser(page: Page) {
  const calls: Array<{ path: string; authorization: string | undefined }> = [];
  browserRequests.set(page, calls);
  page.on("request", request => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith("/core/") || path.startsWith("/v1")) calls.push({ path, authorization: request.headers().authorization });
  });
}
let authenticatedCookies: Awaited<ReturnType<BrowserContext["cookies"]>> = [];
export const account = { username: "admin", password: "correct horse battery" };

/** Reset only synthetic Core data; account state belongs to the real console. */
export async function resetFixture(request: APIRequestContext) {
  expect((await request.post(`${fixture}/__fixture/reset`)).ok()).toBe(true);
}

/** Authenticate using the production account endpoint and its real session cookie. */
export async function openConsole(page: Page, request: APIRequestContext, hash = "overview") {
  await resetFixture(request);
  observeBrowser(page);
  // Reuse a session issued by the real service, avoiding its login rate limit.
  // Browser contexts and synthetic Core data are still fresh for each test.
  await page.context().addCookies(authenticatedCookies);
  const status = await (await page.request.get("/console/auth")).json();
  if (status.mode !== "authenticated") {
    const response = await page.request.post(`/console/auth/${status.mode === "setup" ? "setup" : "login"}`, { headers: { Origin: web }, data: account });
    expect(response.status()).toBe(200);
    authenticatedCookies = await page.context().cookies();
  }
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto(`/#${hash}`);
}

/** Inject a Core response, keeping browser and proxy behavior real. */
export async function failNext(request: APIRequestContext, failure: { method: string; path: string; status: number; code?: string; message?: string; repeat?: boolean }) {
  expect((await request.post(`${fixture}/__fixture/fail-next`, { data: failure })).ok()).toBe(true);
}

export async function requests(request: APIRequestContext) {
  return (await (await request.get(`${fixture}/__fixture/requests`)).json());
}
export async function writes(request: APIRequestContext): Promise<string[]> {
  return (await requests(request)).writes;
}

/** Every Core request comes from the authenticated console, without browser cookies. */
export async function expectManagementBoundary(request: APIRequestContext, page: Page) {
  for (const call of browserRequests.get(page) ?? []) {
    expect(call.path).not.toMatch(/^\/v1(?:\/|$)/);
    expect(call.authorization).toBeUndefined();
  }
  const { violations, calls } = await requests(request);
  expect(violations).toEqual([]);
  for (const call of calls) expect(call).toMatchObject({ authenticated: true, actor: account.username, cookie: null });
}
