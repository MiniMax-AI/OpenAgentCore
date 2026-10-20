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
import { Field, ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { randomUuid } from "@oac/agents-client";
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
  const [directory, setDirectory] = useState(false),
    [search, setSearch] = useState("");
  const cache = useQueryClient();
  const remove = useMutation({
    mutationFn: (id: string) => product(`${kind}/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: [kind] }),
  });
  const Icon = kind === "mcps" ? Plug : Server;
  const actions = (
    <>
      {kind === "mcps" && (
        <Button variant="outline" onClick={() => setDirectory(true)}>
          {tr("浏览目录")}
        </Button>
      )}
      <Button
        onClick={() =>
          setEditing(
            kind === "mcps"
              ? {
                  id: randomUuid(),
                  name: "",
                  label: "",
                  url: "",
                  revision: 0,
                }
              : {
                  id: randomUuid(),
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
    </>
  );
  return (
    <>
      {kind === "runtimes" ? (
        <PageHeader title={title}>{actions}</PageHeader>
      ) : (
        <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-3 px-6 pt-6 pb-1 sm:px-10">
          <h2 className="text-lg font-semibold tracking-tight text-fg">{title}</h2>
          <div className="flex flex-wrap items-center gap-2">{actions}</div>
        </div>
      )}
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
                            id: randomUuid(),
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
                    <p className="mt-1 text-sm text-fg-muted">
                      {item.description}
                    </p>
                    {"authentication" in item && (
                      <p className="mt-1 text-sm">
                        {tr("此服务需要认证，请绑定对应服务的凭据。")}
                      </p>
                    )}
                  </div>
                ))}
            </div>
          </DialogContent>
        </Dialog>
      )}
      <ErrorNotice page error={error || remove.error} />
      <div className="mx-auto w-full max-w-6xl px-6 pb-12 pt-1 sm:px-10">
        {rows.length > 0 ? (
          <div className="divide-y divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
            {rows.map((row) => (
              <article
                key={row.id}
                className="relative flex min-h-[72px] flex-wrap items-center justify-between gap-4 px-6 py-4 transition-colors hover:bg-surface-subtle/60"
              >
                <div className="flex min-w-0 flex-1 items-center gap-4">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-surface-muted">
                    <Icon strokeWidth={1.7} className="h-4 w-4 text-fg-muted" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <h2 className="truncate text-sm font-semibold text-fg">
                      {row.name}
                    </h2>
                    <p className="mt-0.5 truncate text-sm text-fg-muted">
                      {"url" in row
                        ? row.url
                        : row.description || environmentSummary(row)}
                    </p>
                  </div>
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
              </article>
            ))}
          </div>
        ) : (
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
const larkCliSetupScript = `set -eu
export LARK_LAUNCHER="$(command -v lark-cli)"
node <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
(async () => {
  const root = path.dirname(path.dirname(fs.realpathSync(process.env.LARK_LAUNCHER)));
  const version = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8')).version;
  const platform = {linux:'linux', darwin:'darwin'}[process.platform];
  const arch = {x64:'amd64', arm64:'arm64'}[process.arch];
  if (!platform || !arch) throw new Error('Unsupported lark-cli release platform');
  const asset = \`lark-cli-\${version}-\${platform}-\${arch}.tar.gz\`;
  const line = fs.readFileSync(path.join(root, 'checksums.txt'), 'utf8').split('\\n').find(line => line.trim().split(/\\s+/)[1] === asset);
  if (!line) throw new Error('Release checksum unavailable');
  const expected = line.trim().split(/\\s+/)[0];
  const urls = [
    \`https://registry.npmmirror.com/-/binary/lark-cli/v\${version}/\${asset}\`,
    \`https://github.com/larksuite/cli/releases/download/v\${version}/\${asset}\`,
  ];
  let bytes;
  for (const url of urls) {
    try {
      const response = await fetch(url, {signal: AbortSignal.timeout(10000)});
      if (!response.ok) continue;
      const candidate = Buffer.from(await response.arrayBuffer());
      if (crypto.createHash('sha256').update(candidate).digest('hex') !== expected) throw new Error('Release checksum mismatch');
      bytes = candidate;
      break;
    } catch (error) {
      if (error.message === 'Release checksum mismatch') throw error;
    }
  }
  if (!bytes) throw new Error('Could not download verified lark-cli release');
  const directory = fs.mkdtempSync(path.join(require('node:os').tmpdir(), 'lark-download-'));
  try {
    const archive = path.join(directory, asset);
    fs.writeFileSync(archive, bytes);
    execFileSync('tar', ['-xzf', archive, '-C', directory, 'lark-cli']);
    fs.mkdirSync(path.join(root, 'bin'), {recursive: true});
    fs.copyFileSync(path.join(directory, 'lark-cli'), path.join(root, 'bin/lark-cli'));
    fs.chmodSync(path.join(root, 'bin/lark-cli'), 0o755);
  } finally { fs.rmSync(directory, {recursive: true, force: true}); }
  console.log('Verified lark-cli release installed');
})().catch(error => { console.error(error.message); process.exitCode = 1; });
JS
lark-cli --version
printf '%s' "$FEISHU_APP_SECRET" | lark-cli config init --app-id "$FEISHU_APP_ID" --app-secret-stdin --brand "\${FEISHU_BRAND:-feishu}"`;

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
  const [preset, setPreset] = useState("");
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
          {!("url" in form) && !value.revision && (
            <Field label={tr("起始配置")} id="runtime-preset">
              <Select
                id="runtime-preset"
                value={preset}
                onValueChange={(choice) => {
                  setPreset(choice);
                  if (choice === "lark-cli") {
                    setForm({
                      ...form,
                      name: tr("飞书工作环境"),
                      description: tr(
                        "安装官方 lark-cli，并以 Agent 绑定的飞书机器人身份调用开放平台。需要运行节点能够访问飞书开放平台。",
                      ),
                      packages: { npm: ["@larksuite/cli@1.0.98"], python: [] },
                      setup_script: larkCliSetupScript,
                      env: {
                        npm_config_ignore_scripts: "true",
                        LARKSUITE_CLI_DEFAULT_AS: "bot",
                        LARKSUITE_CLI_STRICT_MODE: "bot",
                        LARKSUITE_CLI_NO_UPDATE_NOTIFIER: "1",
                        LARKSUITE_CLI_NO_SKILLS_NOTIFIER: "1",
                      },
                      bot_env: {
                        FEISHU_APP_ID: "app_id",
                        FEISHU_APP_SECRET: "app_secret",
                        FEISHU_BRAND: "brand",
                      },
                      files: [
                        {
                          path: "/workspace/WORKSPACE.md",
                          content:
                            "# 飞书工作环境\n\n官方 lark-cli 已在 Agent 启动前安装，以 Agent 绑定的飞书机器人身份运行。禁止输出 App Secret、令牌或环境变量。读取 Skill 后使用具体业务命令。网络错误与权限不足分别报告。产物写到 ./outputs。\n",
                        },
                      ],
                    });
                  }
                }}
              >
                <SelectOption value="">{tr("自定义")}</SelectOption>
                <SelectOption value="lark-cli">
                  {tr("飞书工作环境（lark-cli）")}
                </SelectOption>
              </Select>
            </Field>
          )}
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
