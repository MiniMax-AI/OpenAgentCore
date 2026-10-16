import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Package, BookOpen, FileText } from "lucide-react";
import { product } from "./lib/product";
import { Models } from "./Models";
import { Credentials } from "./Credentials";
import { Skills } from "./Skills";
import { MCPs } from "./Resources";
import { PageHeader, ErrorNotice, Field } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Select, SelectOption } from "./components/ui/select";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { EmptyState } from "./components/ui/empty-state";
export interface Capability {
  id: string;
  name: string;
  description: string;
  kind: "prompt" | "knowledge" | "plugin";
  revision: number;
  deprecated: boolean;
  default_version: string;
  versions: {
    version: string;
    content?: string;
    files?: string[];
    has_archive?: boolean;
    has_client_bundle?: boolean;
    mcp_servers?: { name: string; transport: string }[];
  }[];
}
export const useCapabilities = () =>
  useQuery({
    queryKey: ["capabilities"],
    queryFn: () => product<Capability[]>("capabilities"),
  });
export function Capabilities({
  initialTab = "models",
}: {
  initialTab?: string;
}) {
  const [tab, setTab] = useState(initialTab);
  return (
    <>
      <PageHeader title={tr("资源库")} />
      <div
        className="flex h-10 shrink-0 items-center gap-1 border-b border-line px-6"
        role="tablist"
        aria-label={tr("能力类型")}
      >
        {[
          ["models", tr("模型")],
          ["skills", "Skills"],
          ["mcps", "MCP"],
          ["plugin", "Plugins"],
          ["prompt", tr("指令片段")],
          ["knowledge", tr("知识")],
          ["credentials", tr("凭据")],
        ].map(([key, label]) => (
          <Button
            key={key}
            role="tab"
            aria-selected={tab === key}
            variant="ghost"
            className={tab === key ? "bg-surface-muted" : "text-fg-muted"}
            onClick={() => setTab(key!)}
          >
            {label}
          </Button>
        ))}
      </div>
      {tab === "models" ? (
        <Models />
      ) : tab === "credentials" ? (
        <Credentials />
      ) : tab === "skills" ? (
        <Skills />
      ) : tab === "mcps" ? (
        <MCPs />
      ) : (
        <CapabilityList key={tab} kind={tab as Capability["kind"]} />
      )}
    </>
  );
}
const labels = {
  prompt: tr("指令片段"),
  knowledge: tr("知识"),
  plugin: "Plugins",
};
function CapabilityList({ kind }: { kind: Capability["kind"] }) {
  const query = useCapabilities();
  const [editing, setEditing] = useState<Capability | null>(null);
  const rows = query.data?.filter((row) => row.kind === kind);
  const Icon =
    kind === "plugin" ? Package : kind === "knowledge" ? BookOpen : FileText;
  return (
    <>
      <PageHeader title={labels[kind]}>
        <Button
          onClick={() =>
            setEditing({
              id: crypto.randomUUID(),
              name: "",
              description: "",
              kind,
              revision: 0,
              deprecated: false,
              default_version: "",
              versions: [],
            })
          }
        >
          <Plus />
          {tr("添加能力")}
        </Button>
      </PageHeader>
      <ErrorNotice error={query.error} />
      <Ledger columns="18px minmax(160px,2fr) minmax(180px,2fr) 90px 90px">
        <LedgerHeader>
          <span />
          <span>{tr("名称")}</span>
          <span>{tr("说明")}</span>
          <span>{tr("默认版本")}</span>
          <span>{tr("状态")}</span>
        </LedgerHeader>
        {rows?.map((row) => (
          <LedgerRow key={row.id}>
            <Icon className="h-3.5 w-3.5 text-fg-muted" />
            <button
              className="truncate text-left font-medium"
              onClick={() => setEditing(row)}
            >
              {row.name}
            </button>
            <span className="text-fg-muted">{row.description}</span>
            <span className="font-mono text-xs">v{row.default_version}</span>
            <span>{row.deprecated ? tr("已停用") : tr("可用")}</span>
          </LedgerRow>
        ))}
        {!rows?.length && <EmptyState title={tr("暂无能力")} />}
      </Ledger>
      {editing && (
        <CapabilityEditor value={editing} close={() => setEditing(null)} />
      )}
    </>
  );
}
export function CapabilityEditor({
  value,
  close,
  onSaved,
}: {
  value: Capability;
  close: () => void;
  onSaved?: (id: string) => void;
}) {
  const cache = useQueryClient(),
    [form, setForm] = useState(value),
    [content, setContent] = useState<string | undefined>(
      value.revision ? undefined : "",
    ),
    [archive, setArchive] = useState(""),
    [clientBundle, setClientBundle] = useState<string | undefined>(),
    [error, setError] = useState("");
  const selected = form.versions.find(
    (version) => version.version === form.default_version,
  );
  const finish = () => {
    void cache.invalidateQueries({ queryKey: ["capabilities"] });
    close();
  };
  const save = useMutation({
    mutationFn: () =>
      product(`capabilities/${form.id}`, "PUT", {
        ...form,
        ...(content !== undefined && form.kind !== "plugin"
          ? { content, default_version: "" }
          : {}),
        ...(archive ? { archive, default_version: "" } : {}),
        ...(clientBundle !== undefined
          ? { client_bundle: clientBundle, default_version: "" }
          : {}),
      }),
    onSuccess: () => {
      onSaved?.(form.id);
      finish();
    },
  });
  const remove = useMutation({
    mutationFn: () =>
      product(`capabilities/${form.id}`, "DELETE", { revision: form.revision }),
    onSuccess: finish,
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
        className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl"
      >
        <DialogHeader>
          <DialogTitle>
            {form.revision ? tr("编辑能力") : tr("添加能力")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="capability-form"
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            save.mutate();
          }}
        >
          <Field label={tr("名称")} id="cap-name">
            <Input
              id="cap-name"
              required
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("说明")} id="cap-description">
            <Input
              id="cap-description"
              value={form.description}
              onChange={(e) =>
                setForm({ ...form, description: e.target.value })
              }
            />
          </Field>
          {form.versions.length > 0 && (
            <Field label={tr("默认版本")} id="cap-version">
              <Select
                id="cap-version"
                value={form.default_version}
                onValueChange={(default_version) => {
                  setForm({ ...form, default_version });
                  setContent(undefined);
                }}
              >
                {form.versions.map((version) => (
                  <SelectOption key={version.version} value={version.version}>
                    v{version.version}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          )}
          {form.kind === "plugin" ? (
            <>
              <Field label={tr("Plugin ZIP（新版本）")} id="cap-archive">
                <Input
                  id="cap-archive"
                  type="file"
                  accept=".zip"
                  onChange={async (e) => {
                    const file = e.target.files?.[0];
                    if (!file) return;
                    if (file.size > 32 * 1024 * 1024)
                      return setError(tr("文件不能超过 32 MiB。"));
                    const reader = new FileReader();
                    reader.onload = () => {
                      setArchive(String(reader.result).split(",")[1] || "");
                      setError("");
                    };
                    reader.onerror = () => setError(tr("无法读取文件。"));
                    reader.readAsDataURL(file);
                  }}
                />
              </Field>
              <Field label={tr("前端扩展 JavaScript（可选）")} id="cap-client">
                <Input
                  id="cap-client"
                  type="file"
                  accept=".js"
                  onChange={async (e) => {
                    const file = e.target.files?.[0];
                    if (!file) return;
                    if (file.size > 1024 * 1024)
                      return setError(tr("前端扩展不能超过 1 MiB。"));
                    setClientBundle(await file.text());
                  }}
                />
              </Field>
              <p className="text-xs text-fg-muted">
                {tr(
                  "仅安装你信任的前端扩展。它以当前页面的权限运行，可访问 Parsar 数据和公开 API。",
                )}
              </p>
              {selected?.has_client_bundle && (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setClientBundle("")}
                >
                  {tr("移除前端扩展")}
                </Button>
              )}
              {selected?.mcp_servers?.map((server) => (
                <p className="text-xs" key={server.name}>
                  {server.name} · {server.transport}
                </p>
              ))}
              {selected?.files && (
                <pre className="max-h-48 overflow-auto text-xs">
                  {selected.files.join("\n")}
                </pre>
              )}
            </>
          ) : (
            <Field label={tr("内容（修改后保存为新版本）")} id="cap-content">
              <Textarea
                id="cap-content"
                rows={12}
                required
                value={content ?? selected?.content ?? ""}
                onChange={(e) => setContent(e.target.value)}
              />
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.deprecated}
              onChange={(e) =>
                setForm({ ...form, deprecated: e.target.checked })
              }
            />
            {tr("停用此能力")}
          </label>
          <ErrorNotice error={error || save.error || remove.error} />
        </form>
        <DialogFooter>
          {value.revision > 0 && (
            <Button
              variant="ghost"
              disabled={remove.isPending || save.isPending}
              onClick={() => {
                if (confirm(tr("删除此能力及其所有版本？"))) remove.mutate();
              }}
            >
              {tr("删除能力")}
            </Button>
          )}
          <Button
            type="submit"
            form="capability-form"
            disabled={save.isPending || remove.isPending}
          >
            {tr("保存能力")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
