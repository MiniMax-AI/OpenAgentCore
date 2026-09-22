import { expect, test } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

test("loads mixed OAuth and static credentials without offering OAuth static replacement", async ({ page, request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
  const vaultId = "11111111-1111-4111-8111-111111111111";
  const vault = { id: vaultId, object: "vault", created_at: 1, name: "Mixed credentials", metadata: {} };
  const oauth = {
    id: "22222222-2222-4222-8222-222222222222", vault_id: vaultId,
    object: "vault.credential", name: "OAuth MCP", created_at: 2, updated_at: 2,
    auth: {
      type: "mcp_oauth", mcp_server_url: "https://oauth.example/tools", expires_at: null,
      refresh: {
        client_id: "public-client-id", token_endpoint: "https://issuer.example/token",
        token_endpoint_auth: { type: "client_secret_basic" }, resource: null, scope: null,
      },
    },
  };
  const staticCredential = {
    id: "33333333-3333-4333-8333-333333333333", vault_id: vaultId,
    object: "vault.credential", name: "Static MCP", created_at: 2, updated_at: 2,
    auth: { type: "static_bearer", mcp_server_url: "https://static.example/tools" },
  };
  await page.route("**/v1/vaults?*", (route) => route.fulfill({ json: {
    object: "list", data: [vault], has_more: false, first_id: vaultId, last_id: vaultId,
  } }));
  await page.route(`**/v1/vaults/${vaultId}/credentials?*`, (route) => route.fulfill({ json: {
    object: "list", data: [oauth, staticCredential], has_more: false, first_id: oauth.id, last_id: staticCredential.id,
  } }));
  await page.goto("/");
  await page.getByRole("button", { name: "Vaults", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Mixed credentials" })).toBeVisible();
  await expect(page.getByText("OAuth MCP", { exact: true })).toBeVisible();
  await expect(page.getByText("OAuth · Manage authorization and token replacement in your application.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Replace token for OAuth MCP" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete OAuth MCP" })).toBeVisible();
  await page.getByRole("button", { name: "Replace token for Static MCP" }).click();
  await expect(page.getByRole("dialog", { name: "Replace token · Static MCP" })).toBeVisible();
});
