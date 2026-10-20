import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { randomUuid } from "@oac/agents-client";
import { Plus, Copy, Trash2, Layers } from "lucide-react";
import {
  product,
  useAgents,
  useModels,
  type AgentProfile,
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
export function Agents() {
  const query = useAgents();
  const models = useModels();
  const cache = useQueryClient();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<AgentProfile | null>(null);

  const remove = useMutation({
    mutationFn: (id: string) => product(`agents/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["agents"] }),
  });
  return (
    <>
      <PageHeader title="Agents">
        <Help>
          Agent 保存模型、指令、Skills 和
          MCP。开始会话时选择运行时，每个会话有独立的工作区和历史。
        </Help>
        <Button
          onClick={() =>
            setEditing({
              ...emptyConfiguration,
              id: randomUuid(),
              name: "",
              revision: 0,
            })
          }
        >
          <Plus />
          新建 Agent
        </Button>
      </PageHeader>
      <ErrorNotice error={query.error || models.error || remove.error} />
      <div className="px-6 py-4">
        <Input
          aria-label="搜索 Agent"
          placeholder="搜索 Agent"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {query.data
            ?.filter((t) => t.name.toLowerCase().includes(search.toLowerCase()))
            .map((agent) => (
              <article
                key={agent.id}
                className="flex flex-col rounded-xl border border-line p-5"
              >
                <div className="flex items-center gap-3">
                  <Layers className="h-5 w-5" />
                  <h2 className="min-w-0 flex-1 truncate text-lg font-semibold">
                    {agent.name}
                  </h2>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`复制 ${agent.name}`}
                    onClick={() =>
                      setEditing({
                        ...agent,
                        id: randomUuid(),
                        name: `${agent.name} 副本`,
                        revision: 0,
                      })
                    }
                  >
                    <Copy />
                  </Button>
                </div>
                <p className="mt-4 line-clamp-3 flex-1 text-base leading-relaxed text-fg-muted">
                  {agent.instructions || "尚未设置指令"}
                </p>
                <div className="mt-5 flex flex-wrap gap-2 text-base">
                  <span className="rounded-md bg-surface-muted px-2 py-1">
                    {models.data?.find((m) => m.id === agent.model_id)?.name ||
                      "模型不可用"}
                  </span>
                  {!!agent.skill_ids.length && (
                    <span className="rounded-md bg-surface-muted px-2 py-1">
                      {agent.skill_ids.length} Skills
                    </span>
                  )}
                  {!!agent.mcp_ids.length && (
                    <span className="rounded-md bg-surface-muted px-2 py-1">
                      {agent.mcp_ids.length} MCP
                    </span>
                  )}
                </div>
                <div className="mt-5 flex justify-between border-t border-line pt-4">
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`删除 ${agent.name}`}
                    disabled={remove.isPending}
                    onClick={() => {
                      if (confirm(`删除 Agent ${agent.name}？`))
                        remove.mutate(agent.id);
                    }}
                  >
                    <Trash2 />
                  </Button>
                  <Button variant="outline" asChild>
                    <a href={`#/agents/${agent.id}`}>打开</a>
                  </Button>
                </div>
              </article>
            ))}
        </div>
        {!query.isPending && !query.data?.length && (
          <EmptyState title="先创建一份可复用的 Agent 配置" />
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
  const [form, setForm] = useState(value);
  const [preset, setPreset] = useState("");
  const save = useMutation({
    mutationFn: () => product(`agents/${form.id}`, "PUT", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["agents"] });
      void cache.invalidateQueries({ queryKey: ["agent", form.id] });
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
        className="max-h-[90dvh] overflow-y-auto sm:max-w-xl"
      >
        <DialogHeader>
          <DialogTitle>
            {value.revision ? "编辑 Agent" : "新建 Agent"}
          </DialogTitle>
        </DialogHeader>
        <form
          id="agent-form"
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {!value.revision && (
            <Field label="起始配置" id="agent-preset">
              <Select
                id="agent-preset"
                value={preset}
                onValueChange={(choice) => {
                  setPreset(choice);
                  const presets: Record<string, [string, string]> = {
                    developer: [
                      "开发助手",
                      "Inspect the code, implement focused changes, run relevant checks and report the outcome.",
                    ],
                    reviewer: [
                      "审查助手",
                      "Review code for actionable correctness and security problems. Cite file locations. Do not modify code unless asked.",
                    ],
                    researcher: [
                      "研究助手",
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
                <SelectOption value="">自定义</SelectOption>
                <SelectOption value="developer">开发助手</SelectOption>
                <SelectOption value="reviewer">审查助手</SelectOption>
                <SelectOption value="researcher">研究助手</SelectOption>
              </Select>
            </Field>
          )}
          <Field label="Agent 名称" id="agent-name">
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
        </form>
        <DialogFooter>
          <Button
            type="submit"
            form="agent-form"
            disabled={save.isPending || !form.model_id}
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
