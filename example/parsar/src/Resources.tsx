import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Pencil, Trash2, Plug, Server } from "lucide-react";
import {
  product,
  useMCPs,
  useRuntimes,
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
      title="运行时"
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
  const cache = useQueryClient();
  const remove = useMutation({
    mutationFn: (id: string) => product(`${kind}/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: [kind] }),
  });
  const Icon = kind === "mcps" ? Plug : Server;
  return (
    <>
      <PageHeader title={title}>
        <Help>
          {kind === "mcps"
            ? "配置 HTTP MCP 服务，Agent可绑定多个服务。当前支持无需认证的 HTTPS MCP；托管会话由工作区连接，纯文本会话由 Core 连接。"
            : "开启会话时选择运行时。用户机器保存平台和工作目录，每个会话单独连接；这里不代表机器在线状态。"}
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
          添加{title}
        </Button>
      </PageHeader>
      <ErrorNotice error={error || remove.error} />
      <div className="min-h-0 flex-1 overflow-auto p-6">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {rows.map((row) => (
            <article key={row.id} className="rounded-xl border border-line p-5">
              <div className="flex items-center gap-3">
                <Icon className="h-5 w-5" />
                <h2 className="min-w-0 flex-1 truncate text-lg font-semibold">
                  {row.name}
                </h2>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`编辑 ${row.name}`}
                  onClick={() => setEditing(row)}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`移除 ${row.name}`}
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(`移除 ${row.name}？`)) remove.mutate(row.id);
                  }}
                >
                  <Trash2 />
                </Button>
              </div>
              <p className="mt-4 break-words text-base text-fg-muted">
                {"url" in row
                  ? row.url
                  : row.environment === "none"
                    ? "纯文本环境"
                    : row.environment === "self_hosted"
                      ? "用户机器 · Linux amd64"
                      : "Core 托管沙箱"}
              </p>
            </article>
          ))}
        </div>
        {!rows.length && <EmptyState title={`添加可供 Agent 使用的${title}`} />}
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
function ResourceEditor({
  kind,
  title,
  value,
  close,
}: {
  kind: string;
  title: string;
  value: MCPProfile | RuntimeProfile;
  close: () => void;
}) {
  const [form, setForm] = useState(value);
  const cache = useQueryClient();
  const save = useMutation({
    mutationFn: () => product(`${kind}/${form.id}`, "PUT", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: [kind] });
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
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {value.revision ? "编辑" : "添加"}
            {title}
          </DialogTitle>
        </DialogHeader>
        <form
          id="resource-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="名称" id="resource-name">
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
              <Field label="服务标识" id="mcp-label">
                <Input
                  id="mcp-label"
                  required
                  pattern="[a-zA-Z0-9_-]+"
                  value={form.label}
                  placeholder="例如 docs"
                  onChange={(e) => setForm({ ...form, label: e.target.value })}
                />
              </Field>
              <Field label="MCP 地址" id="mcp-url">
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
            <>
              <Field label="环境类型" id="runtime-environment">
                <Select
                  id="runtime-environment"
                  value={form.environment}
                  onValueChange={(environment) =>
                    setForm({
                      ...form,
                      environment: environment as RuntimeProfile["environment"],
                    })
                  }
                >
                  <SelectOption value="openai_hosted">
                    Core 托管沙箱
                  </SelectOption>
                  <SelectOption value="self_hosted">用户机器</SelectOption>
                  <SelectOption value="none">纯文本环境</SelectOption>
                </Select>
              </Field>
              {form.environment === "self_hosted" && (
                <>
                  <Field label="机器平台" id="runtime-platform">
                    <Select
                      id="runtime-platform"
                      value={form.platform || ""}
                      onValueChange={(platform) =>
                        setForm({
                          ...form,
                          platform: platform as RuntimeProfile["platform"],
                        })
                      }
                    >
                      <SelectOption value="">选择平台</SelectOption>
                      <SelectOption value="linux">Linux amd64</SelectOption>
                    </Select>
                  </Field>
                  <Field label="工作目录" id="runtime-workspace">
                    <Input
                      id="runtime-workspace"
                      required
                      value={form.workspace_directory || ""}
                      onChange={(e) =>
                        setForm({
                          ...form,
                          workspace_directory: e.target.value,
                        })
                      }
                      placeholder="/home/you/project"
                    />
                  </Field>
                  <Field label="本地能力目录（选填）" id="runtime-capabilities">
                    <Input
                      id="runtime-capabilities"
                      value={form.capability_directories?.[0] || ""}
                      onChange={(e) =>
                        setForm({
                          ...form,
                          capability_directories: e.target.value
                            ? [e.target.value]
                            : [],
                        })
                      }
                      placeholder="包含 Skill 或 Plugin 的绝对目录"
                    />
                  </Field>
                  <div className="flex items-center gap-2 text-base">
                    机器访问权限
                    <Help>
                      daemon
                      以启动用户的权限执行，可访问该用户有权访问的文件和网络。工作目录必须已存在。
                    </Help>
                  </div>
                </>
              )}
            </>
          )}
          <ErrorNotice error={save.error} />
        </form>
        <DialogFooter>
          <Button type="submit" form="resource-form" disabled={save.isPending}>
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
