import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Check, Send, Trash2 } from "lucide-react";
import {
  product,
  useAgents,
  useFeishuBots,
  type AgentProfile,
} from "./lib/product";
import { ConfigurationFields, emptyConfiguration } from "./ConfigurationFields";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { EmptyState } from "./components/ui/empty-state";
import { ErrorNotice, Field, PageHeader, Section } from "./components/shared";
import { randomUuid } from "@oac/agents-client";
import { AgentHealth } from "./AgentHealth";

export function AgentPage({
  id,
  copyFrom,
}: {
  id?: string;
  copyFrom?: string;
}) {
  const agents = useAgents();
  const source = agents.data?.find((row) => row.id === (id || copyFrom));
  if (agents.isPending)
    return <EmptyState className="flex-1" title={tr("正在加载\u2026")} />;
  if (id && !source)
    return <EmptyState className="flex-1" title={tr("Agent 不存在")} />;
  const initial: AgentProfile =
    id && source
      ? source
      : source
        ? {
            ...source,
            id: randomUuid(),
            name: tr("{{value0}} 副本", { value0: source.name }),
            revision: 0,
          }
        : {
            ...emptyConfiguration,
            id: randomUuid(),
            name: "",
            description: "",
            ask_user: true,
            revision: 0,
          };
  return (
    <AgentEditor
      key={initial.id}
      initial={initial}
      draftKey={`parsar-agent-draft:${id || (copyFrom ? `copy:${copyFrom}` : "new")}`}
    />
  );
}

// Unsaved edits survive navigation within the tab; a draft only restores onto
// the revision it was started from.
function readDraft(key: string, revision: number) {
  try {
    const draft = JSON.parse(sessionStorage.getItem(key) || "null");
    return draft?.revision === revision ? (draft.form as AgentProfile) : undefined;
  } catch {
    return undefined;
  }
}

function AgentEditor({
  initial: loaded,
  draftKey,
}: {
  initial: AgentProfile;
  draftKey: string;
}) {
  const cache = useQueryClient();
  const bots = useFeishuBots();
  // The last saved record; later list refreshes never replace an open draft.
  const [initial, setInitial] = useState(loaded);
  const [restored] = useState(() => readDraft(draftKey, loaded.revision));
  const [form, setFormState] = useState(restored || loaded);
  const [attempted, setAttempted] = useState(false);
  const setForm = (next: AgentProfile) => {
    setSaved(false);
    setFormState(next);
    if (JSON.stringify(next) === JSON.stringify(initial))
      sessionStorage.removeItem(draftKey);
    else
      sessionStorage.setItem(
        draftKey,
        JSON.stringify({ revision: initial.revision, form: next }),
      );
  };
  const [saved, setSaved] = useState(false);
  const creating = !initial.revision;
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);
  const missing = {
    name: form.name.trim() ? undefined : tr("请填写名称"),
    model_id: form.model_id ? undefined : tr("请选择模型"),
    runtime_id: form.runtime_id ? undefined : tr("请选择环境模板"),
  };
  const errors: Partial<typeof missing> = attempted ? missing : {};
  const submit = () => {
    if (Object.values(missing).some(Boolean)) {
      setAttempted(true);
      requestAnimationFrame(() => {
        const field = document.querySelector<HTMLElement>('[data-invalid="true"]');
        field?.scrollIntoView({ block: "center", behavior: "smooth" });
        field
          ?.querySelector<HTMLElement>("input, textarea, button")
          ?.focus({ preventScroll: true });
      });
      return;
    }
    save.mutate();
  };
  const bot = bots.data?.find((row) => row.agent_id === form.id);
  const save = useMutation({
    mutationFn: () => product<AgentProfile>(`agents/${form.id}`, "PUT", form),
    onSuccess: (agent) => {
      cache.setQueryData<AgentProfile[]>(["agents"], (rows = []) => [
        agent,
        ...rows.filter((row) => row.id !== agent.id),
      ]);
      void cache.invalidateQueries({ queryKey: ["agents"] });
      sessionStorage.removeItem(draftKey);
      if (creating) location.hash = `/agents/${agent.id}`;
      setInitial(agent);
      setFormState(agent);
      setSaved(true);
    },
  });
  const remove = useMutation({
    mutationFn: () => product(`agents/${form.id}`, "DELETE"),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["agents"] });
      location.hash = "/agents";
    },
  });
  return (
    // Not a <form>: resource editors opened from this page render their own forms in portals.
    <div className="flex flex-col">
      <div className="sticky top-0 z-20 bg-surface/90 backdrop-blur-md">
        <PageHeader
          title={creating ? tr("新建 Agent") : initial.name}
          narrow
          back={
            <Button asChild variant="ghost" size="icon" className="-ml-2">
              <a href="#/agents" aria-label={tr("返回 Agent 列表")}>
                <ArrowLeft />
              </a>
            </Button>
          }
        >
          {!creating && (
            <>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={tr("删除 Agent")}
                title={tr("删除 Agent")}
                disabled={remove.isPending}
                onClick={() => {
                  if (confirm(tr("删除 Agent {{value0}}？", { value0: initial.name })))
                    remove.mutate();
                }}
              >
                <Trash2 />
              </Button>
              <Button asChild variant="ghost">
                <a href={`#/agents/new?from=${initial.id}`}>{tr("复制")}</a>
              </Button>
              <Button asChild variant="outline">
                <a href={`#/new?agent=${initial.id}`}>{tr("开始对话")}</a>
              </Button>
            </>
          )}
          <Button
            type="button"
            onClick={submit}
            disabled={save.isPending || (!creating && !dirty)}
          >
            {saved && !dirty ? <Check /> : null}
            {save.isPending
              ? tr("保存中\u2026")
              : saved && !dirty
                ? tr("已保存")
                : tr("保存")}
          </Button>
        </PageHeader>
      </div>
      <div className="mx-auto w-full max-w-3xl space-y-6 px-6 pb-16 pt-2 sm:px-10">
        <ErrorNotice error={save.error || remove.error} className="my-0" />
        {restored && dirty && (
          <div className="flex items-center justify-between gap-4 rounded-xl bg-surface-subtle px-4 py-3 text-sm">
            <span className="text-fg">{tr("已恢复上次未保存的修改。")}</span>
            <button
              type="button"
              className="shrink-0 font-medium text-fg underline-offset-4 hover:underline"
              onClick={() => {
                setForm(initial);
                setAttempted(false);
              }}
            >
              {tr("放弃修改")}
            </button>
          </div>
        )}
        <Section title={tr("基本信息")}>
          <Field
            label={tr("名称")}
            id="agent-name"
            required
            error={errors.name}
          >
            <Input
              id="agent-name"
              required
              maxLength={80}
              value={form.name}
              placeholder={tr("例如：飞书工作助手")}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("描述")} id="agent-description">
            <Textarea
              id="agent-description"
              rows={2}
              maxLength={500}
              className="min-h-0"
              value={form.description || ""}
              placeholder={tr("一句话说明它擅长做什么")}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
            />
          </Field>
          <div className="flex items-center justify-between gap-4 rounded-xl bg-surface-subtle px-4 py-3.5">
            <span className="flex min-w-0 items-center gap-3 text-sm">
              <Send strokeWidth={1.7} className="h-4 w-4 shrink-0 text-fg-subtle" />
              <span className="text-fg-muted">{tr("飞书机器人")}</span>
              <span className="truncate font-medium text-fg">
                {bot ? bot.name : tr("未绑定")}
              </span>
            </span>
            {creating ? (
              <span className="shrink-0 text-sm text-fg-muted">
                {tr("保存后可绑定")}
              </span>
            ) : (
              <a
                href="#/feishu"
                className="shrink-0 text-sm font-medium text-fg underline-offset-4 hover:underline"
              >
                {bot ? tr("管理") : tr("去绑定")}
              </a>
            )}
          </div>
        </Section>
        {!creating && <AgentHealth id={initial.id} />}
        <ConfigurationFields
          agentId={form.id}
          value={form}
          errors={errors}
          change={(configuration) => setForm({ ...form, ...configuration })}
        />
      </div>
    </div>
  );
}
