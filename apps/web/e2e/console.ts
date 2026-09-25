import { expect, type APIRequestContext, type Page } from "@playwright/test";

const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const web = `http://127.0.0.1:${process.env.AGENTS_WEB_PORT ?? 4174}`;

/** The fixture deployment's Core key; the same value as CORE_KEY in fixture-console.mjs. */
export const FIXTURE_CORE_KEY = "fixture-core-key-3f9a2c71";

/** Fresh fixture state: signed out ("login") or already signed in ("authenticated"). */
export async function resetFixture(request: APIRequestContext, auth: "login" | "authenticated" = "authenticated", options: { fresh?: boolean; sandbox?: "configured" | "none" | "e2b" } = {}) {
  await request.post(`${fixture}/__fixture/reset?auth=${auth}${options.fresh ? "&projects=none" : ""}&sandbox=${options.sandbox ?? "configured"}`);
}

/** Opens the console already signed in, in English. */
export async function openConsole(page: Page, request: APIRequestContext, hash = "overview", options: { sandbox?: "configured" | "none" | "e2b" } = {}) {
  await resetFixture(request, "authenticated", options);
  await page.context().addCookies([{ name: "core_console", value: "fixture-session", url: web }]);
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto(`/#${hash}`);
}

/** Makes the next matching write fail once with the given status. */
export async function failNext(request: APIRequestContext, failure: { method: string; path: string; status: number; code?: string; message?: string }) {
  await request.post(`${fixture}/__fixture/fail-next`, { data: failure });
}

/** Archives a project behind the console's back, as another administrator would. */
export async function archiveProject(request: APIRequestContext, projectId: string) {
  await request.post(`${fixture}/core/v1/projects/${projectId}/archive`, { headers: { cookie: "core_console=fixture-session" } });
}

/** Writes the browser sent through the console, as "METHOD /path". */
export async function writes(request: APIRequestContext): Promise<string[]> {
  return (await (await request.get(`${fixture}/__fixture/requests`)).json()).writes;
}

/** The console never calls /v1 and never sends its own Authorization header. */
export async function expectManagementBoundary(request: APIRequestContext) {
  const { violations } = await (await request.get(`${fixture}/__fixture/requests`)).json();
  expect(violations).toEqual([]);
}
