import type { ExecutorCredentialList } from "@oac/agents-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import i18n from "../../i18n";
import { ExecutorCredentialsSection } from "./ExecutorCredentialsSection";
import { executorConnectionQuery } from "./executor-connection-query";

const project = vi.hoisted(() => ({ archived: false }));
vi.mock("../../lib/projects", async (original) => ({
  ...await original<typeof import("../../lib/projects")>(),
  useProjects: () => ({ byId: new Map([["project", { status: project.archived ? "archived" : "active" }]]), refresh: () => undefined }),
}));
vi.mock("./ExecutorInstallPanel", () => ({
  useExecutorInstall: () => ({ kind: "unavailable" }),
  ExecutorInstallPanel: ({ connected }: { connected: boolean }) => <span data-host-connected={String(connected)} />,
}));

const boundId = "10000000-0000-4000-8000-000000000001";
const newId = "20000000-0000-4000-8000-000000000002";
function read(revoked = false): ExecutorCredentialList {
  return {
    data: [
      { key_id: boundId, created_at: "2026-09-28T01:00:00Z", revoked_at: revoked ? "2026-09-28T02:00:00Z" : null },
      { key_id: newId, created_at: "2026-09-28T03:00:00Z", revoked_at: null },
    ],
    connection: { status: "disconnected", bound_key_id: boundId, enrolled_at: "2026-09-28T01:00:00Z", last_seen_at: "2026-09-28T01:30:00Z" },
  };
}
function render(value: ExecutorCredentialList | null, failed = false) {
  const client = new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false, gcTime: Infinity } } });
  const options = executorConnectionQuery("project", "session", "environment");
  if (value) client.setQueryData(options.queryKey, value);
  else client.getQueryCache().build(client, { queryKey: options.queryKey });
  if (failed) client.getQueryCache().find({ queryKey: options.queryKey })!.setState({ status: "error", error: new Error("unavailable"), errorUpdatedAt: Date.now() + 1_000 });
  const html = renderToStaticMarkup(<QueryClientProvider client={client}><ExecutorCredentialsSection projectId="project" sessionId="session" environmentId="environment" remoteUrl="wss://core.example" /></QueryClientProvider>);
  client.clear();
  return html;
}
afterEach(async () => { project.archived = false; await i18n.changeLanguage("en"); });

describe("executor credential display ownership", () => {
  it("shows binding and its single rotation action only on the matching credential row", () => {
    const html = render(read());
    const panel = html.match(/<section class="executor-connection"[\s\S]*?<\/section>/)?.[0] ?? "";
    expect(panel).not.toContain(boundId);
    expect(panel).not.toContain("Rotate");
    expect(panel).toContain("Refresh · Host connection");
    expect(panel).not.toContain("executor-connection-facts");
    expect(html.match(/aria-label="Copy credential ID"/g)).toHaveLength(2);
    expect(html.match(/aria-label="Rotate bound credential"/g)).toHaveLength(1);
    const rows = html.split("<tr");
    expect(rows.find((row) => row.includes(boundId))).toContain("Bound credential</span>");
    expect(rows.find((row) => row.includes(newId))).not.toContain("Bound credential</span>");
  });

  it("keeps restore on the bound revoked row and hides it for an archived Project", () => {
    const html = render(read(true));
    expect(html.match(/aria-label="Rotate to restore"/g)).toHaveLength(1);
    expect(html).not.toContain(`aria-label="Restore credential`);
    project.archived = true;
    expect(render(read(true))).not.toContain('aria-label="Rotate to restore"');
  });

  it("retains stale binding while disabling its action and withholding host completion", () => {
    const value = read(); value.connection.status = "connected";
    expect(render(value)).toContain('data-host-connected="true"');
    const stale = render(value, true);
    expect(stale).toContain("Refresh failed. Showing the last loaded binding");
    expect(stale).toContain('aria-label="Rotate bound credential" disabled=""');
    expect(stale).toContain('data-host-connected="false"');
    expect(render(null, true)).toContain("The connection could not be read");
    expect(render(null, true)).not.toContain(">Retry</button>");
  });

  it("uses the same row ownership and recovery wording in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = render(read(true));
    expect(html).toContain("绑定凭证</span>");
    expect(html.match(/aria-label="轮换以恢复"/g)).toHaveLength(1);
    expect(html).toContain("刷新 · 主机连接");
    expect(html).toContain('data-host-connected="false"');
  });
});
