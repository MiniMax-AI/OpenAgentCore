import { ListSlot } from "./components/plugin/SlotRenderer";
import { tr } from "./lib/i18n";
import { AgentDetail } from "./AgentDetail";
import { ResizeHandle } from "./components/ui/resize-handle";
import { useResizableWidth } from "./lib/layout-width";
import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Copy, Trash2, Layers } from "lucide-react";
import {
  product,
  useAgents,
  useModels,
  type AgentProfile,
  harnessNames,
} from "./lib/product";
import { ConfigurationFields, emptyConfiguration } from "./ConfigurationFields";
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
import { ErrorNotice, Field, Help, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
const draftKey = "parsar-agent-drafts";
function readAgentDrafts(): Record<string, AgentProfile> {
  try {
    const value = JSON.parse(sessionStorage.getItem(draftKey) || "{}");
    return value && typeof value === "object" && !Array.isArray(value)
      ? value
      : {};
  } catch {
    return {};
  }
}
function writeAgentDraft(form: AgentProfile) {
  try {
    const drafts = readAgentDrafts();
    delete drafts[form.id];
    drafts[form.id] = form;
    sessionStorage.setItem(draftKey, JSON.stringify(drafts));
  } catch {
    /* Editing remains available without browser storage. */
  }
}
function clearAgentDraft(id: string) {
  try {
    const drafts = readAgentDrafts();
    delete drafts[id];
    sessionStorage.setItem(draftKey, JSON.stringify(drafts));
  } catch {
    /* Browser storage may be unavailable. */
  }
}
export function readAgentDraft(id?: string): AgentProfile | null {
  const drafts = readAgentDrafts();
  return (id ? drafts[id] : Object.values(drafts).at(-1)) || null;
}
export function Agents({ selectedId }: { selectedId?: string }) {
  const rail = useResizableWidth({
    storageKey: "agent-rail",
    defaultWidth: 384,
    min: 320,
    max: 720,
    edge: "left",
  });
  const query = useAgents();
  const models = useModels();
  const cache = useQueryClient();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<AgentProfile | null>(() =>
    selectedId ? null : readAgentDraft(),
  );
  const remove = useMutation({
    mutationFn: (id: string) => product(`agents/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["agents"] }),
  });
  return (
    <>
      <PageHeader title={tr("Agent 模板")}>
        <Help>
          {tr(
            "Agent 模板定义职责，绑定模型、Skills 和 MCP，并选择默认环境模板。通过网页或飞书开始任务。",
          )}
        </Help>
        <Button
          onClick={() =>
            setEditing({
              ...emptyConfiguration,
              id: crypto.randomUUID(),
              name: "",
              revision: 0,
            })
          }
        >
          <Plus />
          {tr("新建 Agent")}
        </Button>
      </PageHeader>
      <ErrorNotice error={query.error || models.error || remove.error} />
      {Object.values(readAgentDrafts()).length > 0 && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-2 text-xs">
          <span className="text-fg-muted">{tr("未保存的草稿")}</span>
          {Object.values(readAgentDrafts()).map((draft) => (
            <Button
              key={draft.id}
              variant="ghost"
              onClick={() => setEditing(draft)}
            >
              {tr("恢复草稿")}: {draft.name || tr("新建 Agent")}
            </Button>
          ))}
        </div>
      )}
      <div className="border-b border-line px-6 py-2">
        <Input
          aria-label={tr("搜索 Agent")}
          placeholder={tr("搜索 Agent")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>
      <div className="flex min-h-0 flex-1">
        <Ledger
          columns="18px minmax(220px,2fr) minmax(120px,1fr) minmax(150px,1fr) 100px 110px"
          aria-label={tr("Agents 列表")}
        >
          <LedgerHeader>
            <span />
            <span>Agent</span>
            <span>{tr("引擎")}</span>
            <span>{tr("模型")}</span>
            <span>{tr("能力")}</span>
            <span />
          </LedgerHeader>
          {query.data
            ?.filter((agent) =>
              `${agent.name} ${agent.instructions}`
                .toLowerCase()
                .includes(search.toLowerCase()),
            )
            .map((agent) => (
              <LedgerRow key={agent.id}>
                <Layers className="h-3.5 w-3.5 text-fg-muted" />
                <a
                  className="flex min-w-0 items-center gap-2"
                  href={`#/agents/${agent.id}`}
                >
                  <span className="truncate font-medium">{agent.name}</span>
                  <span className="truncate text-xs text-fg-muted">
                    {agent.instructions}
                  </span>
                </a>
                <span className="text-fg-muted">
                  {harnessNames[agent.harness]}
                </span>
                <span className="font-mono text-xs">
                  {models.data?.find((m) => m.id === agent.model_id)?.name ||
                    tr("模型不可用")}
                </span>
                <span className="text-xs text-fg-muted">
                  {agent.skill_ids.length} Skills · {agent.mcp_ids.length} MCP
                </span>
                <div className="flex items-center justify-end">
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={tr("复制 {{value0}}", { value0: agent.name })}
                    onClick={() =>
                      setEditing({
                        ...agent,
                        id: crypto.randomUUID(),
                        name: tr("{{value0}} 副本", { value0: agent.name }),
                        revision: 0,
                      })
                    }
                  >
                    <Copy />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={tr("删除 {{value0}}", { value0: agent.name })}
                    disabled={remove.isPending}
                    onClick={() => {
                      if (
                        confirm(
                          tr("删除 Agent {{value0}}？", { value0: agent.name }),
                        )
                      )
                        remove.mutate(agent.id);
                    }}
                  >
                    <Trash2 />
                  </Button>
                  <Button variant="ghost" asChild>
                    <a href={`#/agents/${agent.id}`}>{tr("打开")}</a>
                  </Button>
                </div>
              </LedgerRow>
            ))}
          {!query.isPending && !query.data?.length && (
            <EmptyState title={tr("先创建一份可复用的 Agent 配置")} />
          )}
        </Ledger>
        {selectedId && (
          <section
            aria-label={tr("Agent 详情")}
            style={{ width: rail.width }}
            className="relative flex min-h-0 shrink-0 flex-col border-l border-line bg-surface-subtle max-sm:fixed max-sm:inset-0 max-sm:z-30 max-sm:!w-full"
          >
            <AgentDetail key={selectedId} id={selectedId} />
            <ResizeHandle
              edge="left"
              label={tr("调整详情宽度")}
              dragging={rail.dragging}
              {...rail.handleProps}
            />
            {rail.dirty && (
              <div className="absolute bottom-2 right-2 border border-line bg-surface p-2">
                <Button variant="ghost" onClick={rail.save}>
                  {tr("保存宽度")}
                </Button>
                <Button variant="ghost" onClick={rail.restore}>
                  {tr("还原")}
                </Button>
              </div>
            )}
          </section>
        )}
      </div>
      {editing && (
        <AgentEditor value={editing} close={() => setEditing(null)} />
      )}
    </>
  );
}
export function AgentEditor({
  value,
  close,
}: {
  value: AgentProfile;
  close: () => void;
}) {
  const cache = useQueryClient();
  const [form, setForm] = useState(() => {
    const draft = readAgentDraft(value.id);
    return draft?.id === value.id ? draft : value;
  });
  useEffect(() => {
    writeAgentDraft(form);
  }, [form]);
  const [preset, setPreset] = useState("");
  const save = useMutation({
    mutationFn: () => product(`agents/${form.id}`, "PUT", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["agents"] });
      void cache.invalidateQueries({ queryKey: ["agent", form.id] });
      clearAgentDraft(form.id);
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
        className="flex max-h-[90dvh] max-w-[calc(100vw-1.5rem)] flex-col overflow-hidden sm:max-w-2xl lg:max-w-5xl"
      >
        <DialogHeader className="shrink-0">
          <DialogTitle>
            {value.revision ? tr("编辑 Agent") : tr("新建 Agent")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="agent-form"
          className="-mx-4 min-h-0 space-y-5 overflow-y-auto px-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {!value.revision && (
            <Field label={tr("起始配置")} id="agent-preset">
              <Select
                id="agent-preset"
                value={preset}
                onValueChange={(choice) => {
                  setPreset(choice);
                  const presets: Record<string, [string, string]> = {
                    developer: [
                      tr("开发助手"),
                      "Inspect the code, implement focused changes, run relevant checks and report the outcome.",
                    ],
                    reviewer: [
                      tr("审查助手"),
                      "Review code for actionable correctness and security problems. Cite file locations. Do not modify code unless asked.",
                    ],
                    researcher: [
                      tr("研究助手"),
                      "Investigate the question, cite sources and distinguish evidence from assumptions. Deliver a clear report.",
                    ],
                  };
                  const selected = presets[choice];
                  if (selected)
                    setForm({
                      ...form,
                      name: selected[0],
                      instructions: selected[1],
                    });
                }}
              >
                <SelectOption value="">{tr("自定义")}</SelectOption>
                <SelectOption value="developer">{tr("开发助手")}</SelectOption>
                <SelectOption value="reviewer">{tr("审查助手")}</SelectOption>
                <SelectOption value="researcher">{tr("研究助手")}</SelectOption>
              </Select>
            </Field>
          )}
          <Field label={tr("Agent 名称")} id="agent-name">
            <Input
              id="agent-name"
              required
              maxLength={80}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <ConfigurationFields
            value={form}
            change={(configuration) => setForm({ ...form, ...configuration })}
          />
          <ErrorNotice error={save.error} />
          <ListSlot slotId="agent.settings.section" context={{ agent: form }} />
        </form>
        <DialogFooter className="shrink-0">
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              clearAgentDraft(form.id);
              close();
            }}
          >
            {tr("放弃草稿")}
          </Button>
          <Button
            type="submit"
            form="agent-form"
            disabled={save.isPending || !form.model_id}
          >
            {tr("保存")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
