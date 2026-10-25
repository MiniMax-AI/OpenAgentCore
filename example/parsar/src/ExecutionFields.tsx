import { useState } from "react";
import { randomUuid } from "@oac/agents-client";
import {
  harnessOptions,
  executionOptionFields,
  type HarnessKind,
  type ExecutionOptionField,
} from "../shared/harness-options.mjs";
import { tr } from "./lib/i18n";
import {
  harnessNames,
  useModels,
  useProviders,
  type AgentProfile,
  type ModelProfile,
} from "./lib/product";
import { ModelEditor } from "./ModelEditor";
import { ResourceField } from "./components/ResourceField";
import { Field } from "./components/shared";
import { Select, SelectOption } from "./components/ui/select";

type Execution = Pick<AgentProfile, "harness" | "model_id" | ExecutionOptionField>;

export const optionLabels: Record<ExecutionOptionField, string> = {
  reasoning_effort: tr("推理强度"),
  reasoning_summary: tr("推理摘要"),
  web_search: tr("网页搜索"),
  verbosity: tr("输出详细度"),
  service_tier: tr("服务层级"),
};
const optionValues: Record<ExecutionOptionField, Record<string, string>> = {
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
export const optionValue = (field: ExecutionOptionField, value?: string) =>
  value ? optionValues[field][value] || value : tr("默认");

// One line that names the engine, model and the choices that change behavior.
export function engineSummary(agent: Execution, models: ModelProfile[] = []) {
  return [
    harnessNames[agent.harness],
    models.find((row) => row.id === agent.model_id)?.name || tr("模型不可用"),
    agent.reasoning_effort &&
      tr("推理{{value0}}", {
        value0: optionValue("reasoning_effort", agent.reasoning_effort),
      }),
    agent.web_search &&
      agent.web_search !== "disabled" &&
      optionValue("web_search", agent.web_search),
  ]
    .filter(Boolean)
    .join(" · ");
}

function OptionSelect({
  field,
  value,
  change,
  allowDefault = true,
}: {
  field: ExecutionOptionField;
  value: Execution;
  change: (next: string) => void;
  allowDefault?: boolean;
}) {
  return (
    <Field label={optionLabels[field]} id={`execution-${field}`}>
      <Select
        id={`execution-${field}`}
        value={value[field] || ""}
        onValueChange={change}
      >
        {allowDefault && <SelectOption value="">{tr("默认")}</SelectOption>}
        {harnessOptions[value.harness as HarnessKind][field].map((option) => (
          <SelectOption key={option} value={option}>
            {optionValue(field, option)}
          </SelectOption>
        ))}
      </Select>
    </Field>
  );
}

// The engine, model and options that only the selected Harness declares.
export function ExecutionFields<T extends Execution>({
  value,
  change,
  error,
}: {
  value: T;
  change: (value: T) => void;
  error?: string;
}) {
  const models = useModels(),
    providers = useProviders();
  const [cleared, setCleared] = useState<string[]>([]);
  const [modelEditor, setModelEditor] = useState<ModelProfile | null>(null);
  const declared = harnessOptions[value.harness as HarnessKind];
  // Switching engines drops values the new engine does not accept, and says so.
  const changeHarness = (harness: HarnessKind) => {
    const next = { ...value, harness };
    const dropped = executionOptionFields.filter(
      (field) =>
        next[field] &&
        next[field] !== "disabled" &&
        !harnessOptions[harness][field].includes(next[field]!),
    );
    for (const field of dropped)
      next[field] = field === "web_search" ? "disabled" : "";
    setCleared(dropped.map((field) => optionLabels[field]));
    change(next);
  };
  const advanced = (["reasoning_summary", "verbosity", "service_tier"] as const).filter(
    (field) => declared[field].length > (field === "verbosity" ? 1 : 0),
  );
  return (
    <>
      <Field label={tr("执行引擎")} id="execution-harness">
        <Select
          id="execution-harness"
          value={value.harness}
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
        <p
          role="status"
          className="rounded-xl bg-surface-subtle px-4 py-3 text-sm text-fg"
        >
          {tr("{{value0}} 不支持之前的{{value1}}，已恢复为默认。", {
            value0: declared.label,
            value1: cleared.join("、"),
          })}
        </p>
      )}
      <ResourceField
        label={tr("模型")}
        required
        error={error}
        loading={models.isPending || providers.isPending}
        options={(models.data || []).map((row) => ({
          id: row.id,
          name: `${providers.data?.find((provider) => provider.id === row.provider_id)?.name || "Provider"} / ${row.name}`,
          description: row.model,
          disabled: row.enabled === false,
        }))}
        selected={value.model_id ? [value.model_id] : []}
        change={(ids) => change({ ...value, model_id: ids[0] || "" })}
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
          value={value}
          change={(reasoning_effort) => change({ ...value, reasoning_effort })}
        />
        <OptionSelect
          field="web_search"
          value={value}
          allowDefault={false}
          change={(web_search) => change({ ...value, web_search })}
        />
      </div>
      {advanced.length > 0 && (
        <details className="rounded-xl border border-line-muted px-4 py-3">
          <summary className="cursor-pointer list-none text-sm font-medium text-fg">
            {tr("高级")}
          </summary>
          <div className="mt-4 space-y-5">
            {advanced.map((field) => (
              <OptionSelect
                key={field}
                field={field}
                value={value}
                change={(next) => change({ ...value, [field]: next })}
              />
            ))}
          </div>
        </details>
      )}
      {modelEditor && (
        <ModelEditor
          value={modelEditor}
          close={() => setModelEditor(null)}
          onSaved={(model_id) => change({ ...value, model_id })}
        />
      )}
    </>
  );
}
