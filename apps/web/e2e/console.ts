import type { SandboxDeployment, SandboxNodeRollout } from "@oac/agents-client";
import { expect, type APIRequestContext, type Page } from "@playwright/test";

const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const web = `http://127.0.0.1:${process.env.AGENTS_WEB_PORT ?? 4174}`;

/** The fixture deployment's Core key; the same value as CORE_KEY in fixture-console.mjs. */
export const FIXTURE_CORE_KEY = "fixture-core-key-3f9a2c71";

/**
 * Fixture state options: `fresh` is a new install (no project, Session or Runtime),
 * `sandbox` the sandbox deployment, `nodes: "none"` a deployment no node has joined, and
 * `installation` how config.json's public_url is set: "public" (HTTPS, the default), "local"
 * (loopback: only the Core machine reaches the API, and E2B is rejected) or "stale" (public,
 * with a node enrolled with an earlier address), `credentials: "none"` a Core without a
 * credential encryption key, which cannot store a model provider's key, and
 * `installers: "none"` a console without its node installation payload, so it serves neither
 * the node nor the self-hosted installer. `nodeArtifacts` lists the providers the console has
 * node files for, both by default; as in the console, microsandbox needs Docker's files too.
 */
export interface FixtureOptions { fresh?: boolean; sandbox?: "configured" | "none" | "e2b"; nodes?: "none"; installation?: "public" | "local" | "stale"; credentials?: "none"; installers?: "none"; nodeArtifacts?: ("docker" | "microsandbox")[] }

/** Fresh fixture state: signed out ("login") or already signed in ("authenticated"). */
export async function resetFixture(request: APIRequestContext, auth: "login" | "authenticated" = "authenticated", options: FixtureOptions = {}) {
  await request.post(`${fixture}/__fixture/reset?auth=${auth}${options.fresh ? "&projects=none" : ""}&sandbox=${options.sandbox ?? "configured"}${options.nodes ? `&nodes=${options.nodes}` : ""}&installation=${options.installation ?? "public"}${options.credentials ? `&credentials=${options.credentials}` : ""}${options.installers ? `&installers=${options.installers}` : ""}${options.nodeArtifacts ? `&artifacts=${options.nodeArtifacts.join(",")}` : ""}`);
}

const v1Requests: string[] = [];

/** Records and aborts every browser request to the application API (/v1), which the console never calls. */
export async function recordV1Requests(page: Page) {
  await page.route((url) => url.pathname === "/v1" || url.pathname.startsWith("/v1/"), (route) => {
    v1Requests.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    return route.abort();
  });
}

/** Opens the console already signed in, in English. */
export async function openConsole(page: Page, request: APIRequestContext, hash = "overview", options: FixtureOptions = {}) {
  await resetFixture(request, "authenticated", options);
  await recordV1Requests(page);
  await page.context().addCookies([{ name: "core_console", value: "fixture-session", url: web }]);
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto(`/#${hash}`);
}

/** Selects the ready build returned by the E2B discovery fixture. */
export async function selectFixtureE2BBuild(page: Page) {
  await page.getByLabel("E2B API key").fill("fixture-private-key");
  await page.getByLabel("Template", { exact: true }).selectOption("template");
  await page.getByLabel("Template build").selectOption("template:94be54a1-138c-4f30-bc87-b13686272dbe");
}

/** Makes the next matching write fail once with the given status. */
export async function failNext(request: APIRequestContext, failure: { method: string; path: string; status: number; code?: string; message?: string }) {
  await request.post(`${fixture}/__fixture/fail-next`, { data: failure });
}

/** Registers a node, or changes one, as a host running the last enrollment command (or, with its `enrollment_id`, another one) would. */
export async function setNode(request: APIRequestContext, node: { id: string; name?: string; online?: boolean; provider_ready?: boolean; diagnostic?: string; enrollment_id?: string; rollout?: SandboxNodeRollout }) {
  await request.post(`${fixture}/__fixture/node`, { data: node });
}

/** Supplies a later Core observation; only an explicit completion retires nodes and advances generation. */
export async function setDeployment(request: APIRequestContext, deployment: Partial<SandboxDeployment> | { complete_reset: true }): Promise<SandboxDeployment> {
  const response = await request.post(`${fixture}/__fixture/deployment`, { data: deployment });
  expect(response.ok()).toBe(true);
  return response.json() as Promise<SandboxDeployment>;
}

/** Archives a project behind the console's back, as another administrator would. */
export async function archiveProject(request: APIRequestContext, projectId: string) {
  await request.post(`${fixture}/core/v1/projects/${projectId}/archive`, { headers: { cookie: "core_console=fixture-session" } });
}

/** Issues keys in a project behind the console's back, as another administrator would. */
export async function issueKeys(request: APIRequestContext, projectId: string, names: readonly string[]) {
  for (const name of names) await request.post(`${fixture}/core/v1/projects/${projectId}/keys`, { headers: { cookie: "core_console=fixture-session" }, data: { name } });
}

/** Writes the browser sent through the console, as "METHOD /path". */
export async function writes(request: APIRequestContext): Promise<string[]> {
  return (await (await request.get(`${fixture}/__fixture/requests`)).json()).writes;
}

/** The console never calls /v1 and never sends its own Authorization header. */
export async function expectManagementBoundary(request: APIRequestContext) {
  const { violations } = await (await request.get(`${fixture}/__fixture/requests`)).json();
  expect([...v1Requests.splice(0), ...violations]).toEqual([]);
}
