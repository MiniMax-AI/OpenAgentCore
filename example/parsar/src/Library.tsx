import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Search } from "lucide-react";
import { randomUuid } from "@oac/agents-client";
import catalog from "./lib/mcp-catalog.json";
import { tr } from "./lib/i18n";
import { botVariables } from "../shared/bot-bindings.mjs";
import { api } from "./lib/api";
import {
  useAgents,
  useMCPs,
  useSkillCredentials,
  type AgentProfile,
  type MCPProfile,
} from "./lib/product";
import { CapabilityEditor, useCapabilities, type Capability } from "./Capabilities";
import { ResourceEditor } from "./Resources";
import { SkillCreate, SkillDetail, SkillsDirectory, useSkills } from "./Skills";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
import { EmptyState } from "./components/ui/empty-state";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { ErrorNotice } from "./components/shared";

export type CapabilityKind = "skills" | "mcps" | Capability["kind"];
export const kindLabels: Record<CapabilityKind, string> = {
  skills: "Skill",
  mcps: "MCP",
  plugin: "Plugin",
  prompt: tr("指令片段"),
  knowledge: tr("知识"),
};
export type EditorTarget = { kind: CapabilityKind; id: string; creating?: boolean; value?: MCPProfile };

// The editor for one capability, wherever it is opened from.
export function CapabilityEditorFor({
  target,
  close,
  onSaved,
}: {
  target: EditorTarget;
  close: () => void;
  onSaved?: (id: string) => void;
}) {
  const skills = useSkills(),
    mcps = useMCPs(),
    capabilities = useCapabilities();
  if (target.kind === "skills")
    return target.creating ? (
      <SkillCreate close={close} onSaved={onSaved} />
    ) : (
      <SkillDetail skill={skills.data!.find((row) => row.id === target.id)!} close={close} />
    );
  if (target.kind === "mcps")
    return (
      <ResourceEditor
        kind="mcps"
        title="MCP"
        value={
          target.value ||
          (target.creating
            ? { id: target.id, revision: 0, name: "", label: "", url: "" }
            : mcps.data!.find((row) => row.id === target.id)!)
        }
        close={close}
        onSaved={onSaved}
      />
    );
  return (
    <CapabilityEditor
      value={
        target.creating
          ? { id: target.id, name: "", description: "", kind: target.kind, revision: 0, deprecated: false, default_version: "", versions: [] }
          : capabilities.data!.find((row) => row.id === target.id)!
      }
      close={close}
      onSaved={onSaved}
    />
  );
}

// Enabled Agents that bind a capability.
const enabledAgents = (agents: AgentProfile[], kind: CapabilityKind, id: string) =>
  agents.filter(
    (agent) =>
      agent.enabled !== false &&
      (kind === "skills" ? agent.skill_ids : kind === "mcps" ? agent.mcp_ids : agent.capability_ids || []).includes(id),
  ).length;

// Every capability Agents can bind, in one list.
export function Library() {
  const skills = useSkills(),
    mcps = useMCPs(),
    capabilities = useCapabilities(),
    agents = useAgents(),
    skillCredentials = useSkillCredentials();
  const cache = useQueryClient();
  const [kind, setKind] = useState<CapabilityKind | "">("");
  const [search, setSearch] = useState("");
  const [editor, setEditor] = useState<EditorTarget | null>(null);
  const [adding, setAdding] = useState(false);
  const [directory, setDirectory] = useState<"skills" | "mcps" | "">("");
  const upload = useMutation({
    mutationFn: (file: File) => api.uploadSkill({ kind: "zip", file, filename: file.name }),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["skills"] }),
  });
  const all = agents.data || [];
  // The bot's identity variables read as one requirement: the bound Feishu bot.
  const needs = (names: string[]) =>
    [
      ...(names.some((name) => name in botVariables) ? [tr("飞书机器人")] : []),
      ...names.filter((name) => !(name in botVariables)),
    ].join("、") || tr("无需凭据");
  const rows = [
    ...(skills.data || []).map((row) => ({
      kind: "skills" as const,
      id: row.id,
      name: row.name,
      description: row.description,
      version: `v${row.default_version}`,
      credentials: needs(skillCredentials.data?.find((entry) => entry.id === row.id)?.credentials || []),
    })),
    ...(mcps.data || []).map((row) => ({
      kind: "mcps" as const,
      id: row.id,
      name: row.name,
      description: row.url,
      version: "—",
      credentials: row.requires_credential ? tr("凭据库凭据") : tr("无需凭据"),
    })),
    ...(capabilities.data || []).map((row) => ({
      kind: row.kind,
      id: row.id,
      name: row.name,
      description: row.deprecated ? tr("已停用") : row.description,
      version: `v${row.default_version}`,
      credentials: needs(row.credentials || []),
    })),
  ].filter(
    (row) =>
      (!kind || row.kind === kind) &&
      `${row.name} ${row.description}`.toLowerCase().includes(search.toLowerCase()),
  );
  const loading = skills.isPending || mcps.isPending || capabilities.isPending;
  const create = (target: CapabilityKind) => {
    setAdding(false);
    setEditor({ kind: target, id: randomUuid(), creating: true });
  };
  return (
    <>
      <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-3 px-6 pt-6 pb-1 sm:px-10">
        <div role="tablist" aria-label={tr("能力类型")} className="flex flex-wrap gap-2">
          {(["", "skills", "mcps", "plugin", "prompt", "knowledge"] as const).map((value) => (
            <button
              key={value || "all"}
              role="tab"
              aria-selected={kind === value}
              onClick={() => setKind(value)}
              className={
                kind === value
                  ? "h-9 rounded-full bg-fg px-4 text-sm font-medium text-surface"
                  : "h-9 rounded-full px-4 text-sm text-fg-muted hover:bg-surface-muted hover:text-fg"
              }
            >
              {value ? kindLabels[value] : tr("全部")}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-3">
          <div className="relative w-56">
            <Search className="absolute left-3 top-2.5 h-4 w-4 text-fg-muted" />
            <Input
              className="pl-9"
              aria-label={tr("搜索能力")}
              placeholder={tr("搜索能力")}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </div>
          <Button onClick={() => setAdding(true)}>
            <Plus />
            {tr("新建能力")}
          </Button>
        </div>
      </div>
      <ErrorNotice page error={skills.error || mcps.error || capabilities.error || agents.error || upload.error} />
      <Ledger columns="minmax(220px,2fr) 100px 100px 120px minmax(140px,1fr) 48px">
        <LedgerHeader>
          <span>{tr("能力")}</span>
          <span>{tr("类型")}</span>
          <span>{tr("版本")}</span>
          <span>{tr("已启用 Agent")}</span>
          <span>{tr("凭据需求")}</span>
          <span />
        </LedgerHeader>
        {rows.map((row) => (
          <LedgerRow key={`${row.kind}:${row.id}`}>
            <button className="min-w-0 text-left" onClick={() => setEditor({ kind: row.kind, id: row.id })}>
              <span className="block truncate font-medium text-fg">{row.name}</span>
              <span className="block truncate text-fg-muted">{row.description}</span>
            </button>
            <span>{kindLabels[row.kind]}</span>
            <span className="font-mono text-fg-muted">{row.version}</span>
            <span className="tabular-nums">{enabledAgents(all, row.kind, row.id)}</span>
            <span className="truncate text-fg-muted">{row.credentials}</span>
            <Button
              variant="ghost"
              size="icon"
              aria-label={tr("编辑 {{value0}}", { value0: row.name })}
              onClick={() => setEditor({ kind: row.kind, id: row.id })}
            >
              <Pencil />
            </Button>
          </LedgerRow>
        ))}
        {!loading && !rows.length && <EmptyState title={tr("暂无能力")} />}
      </Ledger>
      {adding && (
        <Dialog open onOpenChange={setAdding}>
          <DialogContent aria-describedby={undefined} className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>{tr("新建能力")}</DialogTitle>
            </DialogHeader>
            <div className="grid gap-2">
              <Button variant="outline" className="justify-start" onClick={() => create("mcps")}>{tr("添加 HTTP MCP")}</Button>
              <Button variant="outline" className="justify-start" onClick={() => { setAdding(false); setDirectory("mcps"); }}>{tr("从 MCP 目录添加")}</Button>
              <Button variant="outline" className="justify-start" onClick={() => create("skills")}>{tr("编写 Skill")}</Button>
              <Button variant="outline" className="justify-start" asChild>
                <label className="cursor-pointer">
                  {upload.isPending ? tr("上传中\u2026") : tr("上传 Skill ZIP")}
                  <input
                    aria-label={tr("上传 Skill ZIP")}
                    type="file"
                    accept=".zip"
                    className="sr-only"
                    disabled={upload.isPending}
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      if (file) upload.mutate(file, { onSuccess: () => setAdding(false) });
                      event.target.value = "";
                    }}
                  />
                </label>
              </Button>
              <Button variant="outline" className="justify-start" onClick={() => { setAdding(false); setDirectory("skills"); }}>{tr("从 Skill 目录导入")}</Button>
              <Button variant="outline" className="justify-start" onClick={() => create("plugin")}>{tr("上传 Plugin")}</Button>
              <Button variant="outline" className="justify-start" onClick={() => create("prompt")}>{tr("编写指令片段")}</Button>
              <Button variant="outline" className="justify-start" onClick={() => create("knowledge")}>{tr("添加知识")}</Button>
            </div>
          </DialogContent>
        </Dialog>
      )}
      {directory === "skills" && <SkillsDirectory close={() => setDirectory("")} />}
      {directory === "mcps" && (
        <McpDirectory
          close={() => setDirectory("")}
          pick={(value) => {
            setDirectory("");
            setEditor({ kind: "mcps", id: value.id, creating: true, value });
          }}
        />
      )}
      {editor && <CapabilityEditorFor target={editor} close={() => setEditor(null)} />}
    </>
  );
}

// Known HTTP MCP services; picking one prefills a new MCP.
function McpDirectory({ close, pick }: { close: () => void; pick: (value: MCPProfile) => void }) {
  const [search, setSearch] = useState("");
  return (
    <Dialog open onOpenChange={(open) => !open && close()}>
      <DialogContent aria-describedby={undefined} className="max-h-[85dvh] overflow-auto">
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
            .filter((item) => `${item.name} ${item.description}`.toLowerCase().includes(search.toLowerCase()))
            .map((item) => (
              <div key={item.id} className="flex items-center justify-between gap-4 py-3">
                <div className="min-w-0">
                  <a href={item.homepage_url} target="_blank" rel="noreferrer" className="text-sm font-medium underline">
                    {item.name}
                  </a>
                  <p className="mt-1 text-sm text-fg-muted">{item.description}</p>
                </div>
                <Button
                  variant="outline"
                  onClick={() =>
                    pick({
                      id: randomUuid(),
                      revision: 0,
                      name: item.name,
                      label: item.server.name,
                      url: item.server.url,
                      requires_credential: "authentication" in item,
                    })
                  }
                >
                  {tr("添加服务")}
                </Button>
              </div>
            ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
