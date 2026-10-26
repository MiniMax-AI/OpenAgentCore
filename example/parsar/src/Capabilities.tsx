import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { product } from "./lib/product";
import { Models } from "./Models";
import { Credentials } from "./Credentials";
import { Library } from "./Library";
import { PageHeader, ErrorNotice, Field } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Select, SelectOption } from "./components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
export interface Capability {
  id: string;
  name: string;
  description: string;
  kind: "prompt" | "knowledge" | "plugin";
  // Variables a Plugin's programs need; each Agent supplies them from a secret.
  credentials?: string[];
  revision: number;
  deprecated: boolean;
  default_version: string;
  versions: {
    version: string;
    content?: string;
    files?: string[];
    has_archive?: boolean;
    mcp_servers?: { name: string; transport: string }[];
  }[];
}
export const useCapabilities = () =>
  useQuery({
    queryKey: ["capabilities"],
    queryFn: () => product<Capability[]>("capabilities"),
  });
export function Capabilities({
  initialTab = "capabilities",
}: {
  initialTab?: string;
}) {
  // Each tab is a route, so reloading or going back keeps it.
  const tab = initialTab;
  const tabs: [string, string][] = [
    ["capabilities", tr("能力")],
    ["models", tr("模型")],
    ["credentials", tr("凭据")],
  ];
  return (
    <>
      <PageHeader title={tr("资源库")} />
      <div className="mx-auto w-full max-w-6xl shrink-0 px-6 sm:px-10">
        <div
          className="flex items-center gap-6 overflow-x-auto border-b border-line"
          role="tablist"
          aria-label={tr("资源库")}
        >
          {tabs.map(([key, label]) => (
            <button
              key={key}
              role="tab"
              aria-selected={tab === key}
              className={
                tab === key
                  ? "-mb-px h-10 shrink-0 border-b-2 border-fg text-sm font-medium text-fg"
                  : "-mb-px h-10 shrink-0 border-b-2 border-transparent text-sm text-fg-muted hover:text-fg"
              }
              onClick={() => (location.hash = `#/${key}`)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      {tab === "models" ? <Models /> : tab === "credentials" ? <Credentials /> : <Library />}
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
              {selected?.mcp_servers?.map((server) => (
                <p className="text-sm" key={server.name}>
                  {server.name} · {server.transport}
                </p>
              ))}
              {selected?.files && (
                <pre className="max-h-48 overflow-auto rounded-xl bg-surface-subtle p-4 text-sm">
                  {selected.files.join("\n")}
                </pre>
              )}
              <CredentialNames
                value={form.credentials || []}
                change={(credentials) => setForm({ ...form, credentials })}
              />
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

// The variables a capability needs, one per line. Each Agent that binds it
// chooses which secret supplies each.
export function CredentialNames({
  value,
  change,
}: {
  value: string[];
  change: (names: string[]) => void;
}) {
  return (
    <Field label={tr("需要的密钥（环境变量名，每行一个）")} id="capability-credentials">
      <Textarea
        id="capability-credentials"
        rows={3}
        className="font-mono"
        placeholder="GITHUB_TOKEN"
        value={value.join("\n")}
        onChange={(event) =>
          change(event.target.value.split("\n").map((line) => line.trim()).filter(Boolean))
        }
      />
    </Field>
  );
}
