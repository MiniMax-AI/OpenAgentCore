import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { randomUuid } from "@oac/agents-client";
import { Pencil, Plus, SlidersHorizontal, Trash2 } from "lucide-react";
import {
  harnessOptions,
  profileOptionFields,
  type HarnessKind,
  type ProfileOptionField,
} from "../shared/harness-options.mjs";
import { tr } from "./lib/i18n";
import {
  harnessNames,
  product,
  useAgents,
  useModels,
  useProfiles,
  useProviders,
  type ExecutionProfile,
  type ModelProfile,
} from "./lib/product";
import { ModelEditor } from "./ModelEditor";
import { ResourceField } from "./components/ResourceField";
import { ErrorNotice, Field, PageHeader } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";

export const optionLabels: Record<ProfileOptionField, string> = {
  reasoning_effort: tr("推理强度"),
  reasoning_summary: tr("推理摘要"),
  web_search: tr("网页搜索"),
  verbosity: tr("输出详细度"),
  service_tier: tr("服务层级"),
};
const optionValues: Record<ProfileOptionField, Record<string, string>> = {
  reasoning_effort: {
    none: tr("不推理"),
    minimal: tr("极少"),
    low: tr("低"),
    medium: tr("中"),
    high: tr("高"),
    xhigh: tr("很高"),
    max: tr("最高"),
  },
  reasoning_summary: {
    concise: tr("简洁"),
    detailed: tr("详细"),
    auto: tr("自动"),
  },
  web_search: {
    disabled: tr("关闭"),
    cached: tr("使用缓存"),
    live: tr("实时搜索"),
  },
  verbosity: { low: tr("简短"), medium: tr("适中"), high: tr("详细") },
  service_tier: {
    default: tr("标准"),
    flex: tr("弹性"),
    priority: tr("优先"),
  },
};
export const optionValue = (field: ProfileOptionField, value?: string) =>
  value ? optionValues[field][value] || value : tr("默认");

// One line that names the engine, model and the choices that change behavior.
export function profileSummary(
  profile: ExecutionProfile | undefined,
  models: ModelProfile[] = [],
) {
  if (!profile) return tr("未选择执行配置");
  return [
    harnessNames[profile.harness],
    models.find((row) => row.id === profile.model_id)?.name || tr("模型不可用"),
    profile.reasoning_effort &&
      tr("推理{{value0}}", {
        value0: optionValue("reasoning_effort", profile.reasoning_effort),
      }),
    profile.web_search &&
      profile.web_search !== "disabled" &&
      optionValue("web_search", profile.web_search),
  ]
    .filter(Boolean)
    .join(" · ");
}

export const emptyProfile = (): ExecutionProfile => ({
  id: randomUuid(),
  name: "",
  revision: 0,
  harness: "codex",
  model_id: "",
  reasoning_effort: "",
  reasoning_summary: "",
  web_search: "disabled",
  verbosity: "",
  service_tier: "",
});

export function Profiles() {
  const profiles = useProfiles(),
    agents = useAgents(),
    models = useModels();
  const cache = useQueryClient();
  const [editing, setEditing] = useState<ExecutionProfile | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => product(`profiles/${id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["profiles"] }),
  });
  const users = (id: string) =>
    (agents.data || []).filter((agent) => agent.profile_id === id).length;
  return (
    <>
      <PageHeader title={tr("执行配置")}>
        <Button onClick={() => setEditing(emptyProfile())}>
          <Plus />
          {tr("添加执行配置")}
        </Button>
      </PageHeader>
      <ErrorNotice page error={profiles.error || remove.error} />
      <div className="mx-auto w-full max-w-6xl px-6 pb-12 pt-1 sm:px-10">
        {profiles.data?.length ? (
          <div className="divide-y divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
            {profiles.data.map((row) => {
              const used = users(row.id);
              return (
                <article
                  key={row.id}
                  className="flex min-h-[72px] items-center gap-4 px-6 py-4 transition-colors hover:bg-surface-subtle/60"
                >
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-surface-muted">
                    <SlidersHorizontal
                      strokeWidth={1.7}
                      className="h-4 w-4 text-fg-muted"
                    />
                  </span>
                  <div className="min-w-0 flex-1">
                    <h2 className="truncate text-sm font-semibold text-fg">
                      {row.name}
                    </h2>
                    {profileSummary(row, models.data) !== row.name && (
                      <p className="mt-0.5 truncate text-sm text-fg-muted">
                        {profileSummary(row, models.data)}
                      </p>
                    )}
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
                    title={
                      used
                        ? tr("仍被 {{count}} 个 Agent 使用，不能移除", { count: used })
                        : undefined
                    }
                    disabled={!!used || remove.isPending}
                    onClick={() => {
                      if (confirm(tr("移除 {{value0}}？", { value0: row.name })))
                        remove.mutate(row.id);
                    }}
                  >
                    <Trash2 />
                  </Button>
                </article>
              );
            })}
          </div>
        ) : (
          !profiles.isPending && (
            <EmptyState
              title={tr("还没有执行配置")}
              description={tr("选择执行引擎、模型和推理方式，供 Agent 引用。")}
            />
          )
        )}
      </div>
      {editing && (
        <ProfileEditor value={editing} close={() => setEditing(null)} />
      )}
    </>
  );
}

function OptionSelect({
  field,
  form,
  change,
  allowDefault = true,
}: {
  field: ProfileOptionField;
  form: ExecutionProfile;
  change: (value: string) => void;
  allowDefault?: boolean;
}) {
  return (
    <Field label={optionLabels[field]} id={`profile-${field}`}>
      <Select
        id={`profile-${field}`}
        value={form[field] || ""}
        onValueChange={change}
      >
        {allowDefault && <SelectOption value="">{tr("默认")}</SelectOption>}
        {harnessOptions[form.harness as HarnessKind][field].map((value) => (
          <SelectOption key={value} value={value}>
            {optionValue(field, value)}
          </SelectOption>
        ))}
      </Select>
    </Field>
  );
}

export function ProfileEditor({
  value,
  close,
  onSaved,
}: {
  value: ExecutionProfile;
  close: () => void;
  onSaved?: (id: string) => void;
}) {
  const cache = useQueryClient();
  const models = useModels(),
    providers = useProviders();
  const [form, setForm] = useState(value);
  const [cleared, setCleared] = useState<string[]>([]);
  const [attempted, setAttempted] = useState(false);
  const [modelEditor, setModelEditor] = useState<ModelProfile | null>(null);
  const declared = harnessOptions[form.harness as HarnessKind];
  const missing = {
    name: form.name.trim() ? undefined : tr("请填写名称"),
    model_id: form.model_id ? undefined : tr("请选择模型"),
  };
  const errors: Partial<typeof missing> = attempted ? missing : {};
  const save = useMutation({
    mutationFn: () => product<ExecutionProfile>(`profiles/${form.id}`, "PUT", form),
    onSuccess: (profile) => {
      void cache.invalidateQueries({ queryKey: ["profiles"] });
      onSaved?.(profile.id);
      close();
    },
  });
  // Switching engines drops values the new engine does not accept, and says so.
  const changeHarness = (harness: HarnessKind) => {
    const next = { ...form, harness };
    const dropped = profileOptionFields.filter(
      (field) =>
        next[field] &&
        next[field] !== "disabled" &&
        !harnessOptions[harness][field].includes(next[field]!),
    );
    for (const field of dropped)
      next[field] = field === "web_search" ? "disabled" : "";
    setCleared(dropped.map((field) => optionLabels[field]));
    setForm(next);
  };
  const advanced = (["reasoning_summary", "verbosity", "service_tier"] as const).filter(
    (field) => declared[field].length > (field === "verbosity" ? 1 : 0),
  );
  return (
    <Dialog open onOpenChange={(open) => !open && !save.isPending && close()}>
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] overflow-y-auto sm:max-w-xl"
      >
        <DialogHeader>
          <DialogTitle>
            {value.revision ? tr("编辑执行配置") : tr("添加执行配置")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="profile-form"
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            if (Object.values(missing).some(Boolean)) setAttempted(true);
            else save.mutate();
          }}
        >
          <Field label={tr("名称")} id="profile-name" required error={errors.name}>
            <Input
              id="profile-name"
              maxLength={80}
              value={form.name}
              placeholder={tr("例如：Codex · 深度推理")}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
            />
          </Field>
          <Field label={tr("执行引擎")} id="profile-harness">
            <Select
              id="profile-harness"
              value={form.harness}
              onValueChange={(harness) => changeHarness(harness as HarnessKind)}
            >
              {(Object.keys(harnessOptions) as HarnessKind[]).map((key) => (
                <SelectOption key={key} value={key}>
                  {harnessOptions[key].label}
                </SelectOption>
              ))}
            </Select>
          </Field>
          {cleared.length > 0 && (
            <p role="status" className="rounded-xl bg-surface-subtle px-4 py-3 text-sm text-fg">
              {tr("{{value0}} 不支持之前的{{value1}}，已恢复为默认。", {
                value0: declared.label,
                value1: cleared.join("、"),
              })}
            </p>
          )}
          <ResourceField
            label={tr("模型")}
            required
            error={errors.model_id}
            loading={models.isPending || providers.isPending}
            options={(models.data || []).map((row) => ({
              id: row.id,
              name: `${providers.data?.find((provider) => provider.id === row.provider_id)?.name || "Provider"} / ${row.name}`,
              description: row.model,
              disabled: row.enabled === false,
            }))}
            selected={form.model_id ? [form.model_id] : []}
            change={(ids) => setForm({ ...form, model_id: ids[0] || "" })}
            empty={tr("选择模型")}
            create={() =>
              setModelEditor({
                id: randomUuid(),
                revision: 0,
                name: "",
                model: "",
                provider_id: "",
              })
            }
            createLabel={tr("添加模型")}
            inspect={(id) =>
              setModelEditor(models.data?.find((row) => row.id === id) || null)
            }
          />
          <div className="grid gap-5 sm:grid-cols-2">
            <OptionSelect
              field="reasoning_effort"
              form={form}
              change={(reasoning_effort) => setForm({ ...form, reasoning_effort })}
            />
            <OptionSelect
              field="web_search"
              form={form}
              allowDefault={false}
              change={(web_search) => setForm({ ...form, web_search })}
            />
          </div>
          <details className="group rounded-xl border border-line-muted px-4 py-3">
            <summary className="cursor-pointer list-none text-sm font-medium text-fg">
              {tr("高级")}
            </summary>
            <div className="mt-4 space-y-5">
              {advanced.length ? (
                advanced.map((field) => (
                  <OptionSelect
                    key={field}
                    field={field}
                    form={form}
                    change={(next) => setForm({ ...form, [field]: next })}
                  />
                ))
              ) : null}
              <Field label={tr("描述")} id="profile-description">
                <Input
                  id="profile-description"
                  maxLength={500}
                  value={form.description || ""}
                  onChange={(event) =>
                    setForm({ ...form, description: event.target.value })
                  }
                />
              </Field>
            </div>
          </details>
          <ErrorNotice error={save.error} className="my-0" />
        </form>
        <DialogFooter>
          <Button type="submit" form="profile-form" disabled={save.isPending}>
            {save.isPending ? tr("保存中\u2026") : tr("保存")}
          </Button>
        </DialogFooter>
        {modelEditor && (
          <ModelEditor
            value={modelEditor}
            close={() => setModelEditor(null)}
            onSaved={(model_id) => setForm((current) => ({ ...current, model_id }))}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
