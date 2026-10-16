import { EnvironmentFields, environmentSummary } from "./EnvironmentFields";
import catalog from "./lib/mcp-catalog.json";
import { tr } from "./lib/i18n";
import { useVaults } from "./Credentials";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Pencil, Trash2, Plug, Server } from "lucide-react";
import {
  product,
  useMCPs,
  useRuntimes,
  useAgents,
  type MCPProfile,
  type RuntimeProfile,
} from "./lib/product";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { Field, Help, ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
export function MCPs() {
  const query = useMCPs();
  return (
    <ResourceList
      kind="mcps"
      title="MCP"
      rows={query.data || []}
      error={query.error}
    />
  );
}
export function Runtimes() {
  const query = useRuntimes();
  return (
    <ResourceList
      kind="runtimes"
      title={tr("环境模板")}
      rows={query.data || []}
      error={query.error}
    />
  );
}
function ResourceList({
  kind,
  title,
  rows,
  error,
}: {
  kind: "mcps" | "runtimes";
  title: string;
  rows: (MCPProfile | RuntimeProfile)[];
  error: unknown;
}) {
  const [editing, setEditing] = useState<MCPProfile | RuntimeProfile | null>(
    null,
  );
  const agents = useAgents();
  const [directory, setDirectory] = useState(false),
    [search, setSearch] = useState("");
  const cache = useQueryClient();
  const remove = useMutation({
    mutationFn: (id: string) => product(`${kind}/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: [kind] }),
  });
  const Icon = kind === "mcps" ? Plug : Server;
  return (
    <>
      <PageHeader title={title}>
        {kind === "mcps" && (
          <Button variant="outline" onClick={() => setDirectory(true)}>
            {tr("浏览目录")}
          </Button>
        )}
        <Help>
          {kind === "mcps"
            ? tr(
                "配置 HTTPS MCP 服务，可绑定凭据库中的凭据。Agent 从工作环境连接服务。",
              )
            : tr(
                "环境模板定义任务开始前需要准备的软件包、文件、变量和初始化脚本。将模板绑定到 Agent 后，每个新会话自动准备。",
              )}
        </Help>
        <Button
          onClick={() =>
            setEditing(
              kind === "mcps"
                ? {
                    id: crypto.randomUUID(),
                    name: "",
                    label: "",
                    url: "",
                    revision: 0,
                  }
                : {
                    id: crypto.randomUUID(),
                    name: "",
                    environment: "openai_hosted",
                    revision: 0,
                  },
            )
          }
        >
          <Plus />
          {tr("添加")}
          {title}
        </Button>
      </PageHeader>
      {directory && (
        <Dialog open onOpenChange={setDirectory}>
          <DialogContent
            aria-describedby={undefined}
            className="max-h-[85dvh] overflow-auto"
          >
            <DialogHeader>
              <DialogTitle>{tr("MCP 目录")}</DialogTitle>
            </DialogHeader>
            <Input
              aria-label={tr("搜索 MCP 目录")}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={tr("搜索服务")}
            />
            <div className="divide-y divide-line">
              {catalog.items
                .filter((item) =>
                  `${item.name} ${item.description}`
                    .toLowerCase()
                    .includes(search.toLowerCase()),
                )
                .map((item) => (
                  <div key={item.id} className="py-3">
                    <div className="flex items-center justify-between">
                      <a
                        href={item.homepage_url}
                        target="_blank"
                        rel="noreferrer"
                        className="text-sm font-medium underline"
                      >
                        {item.name}
                      </a>
                      <Button
                        variant="outline"
                        onClick={() => {
                          setDirectory(false);
                          setEditing({
                            id: crypto.randomUUID(),
                            revision: 0,
                            name: item.name,
                            label: item.server.name,
                            url: item.server.url,
                          });
                        }}
                      >
                        {tr("添加服务")}
                      </Button>
                    </div>
                    <p className="mt-1 text-xs text-fg-muted">
                      {item.description}
                    </p>
                    {"authentication" in item && (
                      <p className="mt-1 text-xs">
                        {tr("此服务需要认证，请绑定对应服务的凭据。")}
                      </p>
                    )}
                  </div>
                ))}
            </div>
          </DialogContent>
        </Dialog>
      )}
      <ErrorNotice error={error || remove.error} />
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="divide-y divide-line-muted">
          {rows.map((row) => (
            <article
              key={row.id}
              className="relative flex min-h-9 items-center gap-6 px-6"
            >
              <div className="flex min-w-0 flex-1 items-center gap-3">
                <Icon className="h-3.5 w-3.5" />
                <h2 className="min-w-0 flex-1 truncate text-sm font-medium">
                  {row.name}
                </h2>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={tr("编辑 {{value0}}", { value0: row.name })}
                  onClick={() => setEditing(row)}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={tr("移除 {{value0}}", { value0: row.name })}
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(tr("移除 {{value0}}？", { value0: row.name })))
                      remove.mutate(row.id);
                  }}
                >
                  <Trash2 />
                </Button>
              </div>
              <p className="w-1/3 truncate text-xs text-fg-muted">
                {"url" in row ? row.url : environmentSummary(row)}
              </p>
              {"environment" in row && (
                <div className="w-1/4 text-xs text-fg-muted">
                  <p className="truncate">{row.description}</p>
                  <p>
                    {tr("引用此模板的 Agent")}:{" "}
                    {agents.data
                      ?.filter((agent) => agent.runtime_id === row.id)
                      .map((agent) => agent.name)
                      .join(", ") || "—"}
                  </p>
                </div>
              )}
            </article>
          ))}
        </div>
        {!rows.length && (
          <EmptyState
            title={tr("添加可供 Agent 使用的{{value0}}", { value0: title })}
          />
        )}
      </div>
      {editing && (
        <ResourceEditor
          kind={kind}
          title={title}
          value={editing}
          close={() => setEditing(null)}
        />
      )}
    </>
  );
}
export function ResourceEditor({
  kind,
  title,
  value,
  close,
  onSaved,
}: {
  onSaved?: (id: string) => void;
  kind: string;
  title: string;
  value: MCPProfile | RuntimeProfile;
  close: () => void;
}) {
  const vaults = useVaults();
  const [form, setForm] = useState(value);
  const cache = useQueryClient();
  const save = useMutation({
    mutationFn: () =>
      product(
        `${kind}/${form.id}`,
        "PUT",
        "environment" in form
          ? {
              ...form,
              packages: Object.fromEntries(
                Object.entries(form.packages || {}).map(([manager, values]) => [
                  manager,
                  values.map((v) => v.trim()).filter(Boolean),
                ]),
              ),
            }
          : form,
      ),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: [kind] });
      onSaved?.(form.id);
      close();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] max-w-2xl overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>
            {value.revision ? tr("编辑") : tr("添加")}
            {title}
          </DialogTitle>
        </DialogHeader>
        <form
          id="resource-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            e.stopPropagation();
            save.mutate();
          }}
        >
          <Field label={tr("名称")} id="resource-name">
            <Input
              required
              id="resource-name"
              value={form.name}
              maxLength={80}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          {"url" in form ? (
            <>
              <Field label={tr("凭据库")} id="mcp-vault">
                <Select
                  id="mcp-vault"
                  value={form.vault_id || ""}
                  onValueChange={(vault_id) =>
                    setForm({ ...form, vault_id, credential_id: "" })
                  }
                >
                  <SelectOption value="">{tr("无需认证")}</SelectOption>
                  {vaults.data?.map((vault) => (
                    <SelectOption key={vault.id} value={vault.id}>
                      {vault.name || vault.id}
                    </SelectOption>
                  ))}
                </Select>
              </Field>
              <Field label={tr("服务标识")} id="mcp-label">
                <Input
                  id="mcp-label"
                  required
                  pattern="[a-zA-Z0-9_-]+"
                  value={form.label}
                  placeholder={tr("例如 docs")}
                  onChange={(e) => setForm({ ...form, label: e.target.value })}
                />
              </Field>
              <Field label={tr("MCP 地址")} id="mcp-url">
                <Input
                  id="mcp-url"
                  required
                  type="url"
                  value={form.url}
                  placeholder="https://example.com/mcp"
                  onChange={(e) => setForm({ ...form, url: e.target.value })}
                />
              </Field>
            </>
          ) : (
            <EnvironmentFields value={form} onChange={setForm} />
          )}
          <ErrorNotice error={save.error} />
        </form>
        <DialogFooter>
          <Button type="submit" form="resource-form" disabled={save.isPending}>
            {tr("保存")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
