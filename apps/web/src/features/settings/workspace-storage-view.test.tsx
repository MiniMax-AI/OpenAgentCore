import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";
import i18n from "../../i18n";
import { WorkspaceStorageSettings } from "./WorkspaceStorageSettings";
import { workspaceStorageQuery } from "./workspace-storage";

const selection = { id: "11111111-1111-4111-8111-111111111111", adapter: "independent-adapter", parameters: { root: "/srv/workspaces" }, tenant_capacity_mib: 4096 };
function render(value: typeof selection | null) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(workspaceStorageQuery.queryKey, value);
  const result = renderToStaticMarkup(<QueryClientProvider client={client}><WorkspaceStorageSettings /></QueryClientProvider>);
  client.clear();
  return result;
}
afterEach(() => { void i18n.changeLanguage("en"); });
describe("workspace storage settings", () => {
  it("shows the stored adapter without guessing its capabilities and offers separate capacity editing", () => {
    const html = render(selection);
    expect(html).toContain("independent-adapter");
    expect(html).toContain("Edit tenant capacity");
    expect(html).toContain("Select new configuration");
    expect(html).not.toContain('type="password"');
    expect(html).not.toContain("nfs");
  });
  it("offers initial configuration without an editable capacity for an absent selection", () => {
    const html = render(null);
    expect(html).toContain("Independent workspace storage is not configured.");
    expect(html).toContain("Select new configuration");
    expect(html).not.toContain("Edit tenant capacity");
  });
  it("renders the same actions in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = render(selection);
    expect(html).toContain("修改租户容量");
    expect(html).toContain("选择新配置");
  });
});
