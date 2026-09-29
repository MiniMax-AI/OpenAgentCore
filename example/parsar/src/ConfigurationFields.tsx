import {
  useModels,
  useProviders,
  useMCPs,
  type AgentProfile,
} from "./lib/product";
import { useSkills } from "./Skills";
import { Select, SelectOption } from "./components/ui/select";
import { Textarea } from "./components/ui/textarea";
import { ErrorNotice, Field, Help } from "./components/shared";
export type Configuration = Pick<
  AgentProfile,
  "model_id" | "harness" | "instructions" | "skill_ids" | "mcp_ids"
>;
export const emptyConfiguration: Configuration = {
  model_id: "",
  harness: "claude_sdk",
  instructions: "",
  skill_ids: [],
  mcp_ids: [],
};
export function ConfigurationFields({
  value,
  change,
}: {
  value: Configuration;
  change: (value: Configuration) => void;
}) {
  const models = useModels();
  const providers = useProviders();
  const skills = useSkills();
  const mcps = useMCPs();
  const toggle = (key: "skill_ids" | "mcp_ids", id: string, checked: boolean) =>
    change({
      ...value,
      [key]: checked ? [...value[key], id] : value[key].filter((v) => v !== id),
    });
  return (
    <>
      <Field label="模型" id="config-model">
        <Select
          id="config-model"
          value={value.model_id}
          onValueChange={(model_id) => change({ ...value, model_id })}
        >
          <SelectOption value="">选择模型</SelectOption>
          {providers.data?.flatMap(
            (provider) =>
              models.data
                ?.filter((model) => model.provider_id === provider.id)
                .map((model) => (
                  <SelectOption key={model.id} value={model.id}>
                    {provider.name} / {model.name}
                  </SelectOption>
                )) || [],
          )}
        </Select>
        {!models.isPending && !models.data?.length && (
          <a href="#/models" className="inline-block text-base underline">
            前往添加模型
          </a>
        )}
      </Field>
      <Field label="执行引擎" id="config-harness">
        <Select
          id="config-harness"
          value={value.harness}
          onValueChange={(harness) =>
            change({ ...value, harness: harness as Configuration["harness"] })
          }
        >
          <SelectOption value="claude_sdk">Claude Code</SelectOption>
          <SelectOption value="codex">Codex</SelectOption>
          <SelectOption value="mcode">MiniMax Code</SelectOption>
        </Select>
      </Field>
      <Field label="指令" id="config-instructions">
        <Textarea
          id="config-instructions"
          rows={6}
          value={value.instructions}
          onChange={(e) => change({ ...value, instructions: e.target.value })}
        />
      </Field>
      {(
        [
          { key: "skill_ids", label: "Skills", rows: skills.data },
          { key: "mcp_ids", label: "MCP", rows: mcps.data },
        ] as const
      ).map((group) => (
        <section key={group.key} className="space-y-2">
          <div className="flex items-center gap-2">
            <h3 className="text-base font-medium">{group.label}</h3>
            <Help>
              {group.key === "skill_ids"
                ? "最多选择 8 个技能，运行时需支持托管沙箱。新会话使用技能的默认版本。"
                : "最多选择 8 个 MCP。托管会话从工作区连接，纯文本会话从 Core 连接；用户机器请在本地 Plugin 中配置。"}
            </Help>
          </div>
          <div className="max-h-48 space-y-2 overflow-auto">
            {group.rows?.map((row) => (
              <label
                key={row.id}
                className="flex items-center gap-3 rounded-lg border border-line px-3 py-2 text-base"
              >
                <input
                  type="checkbox"
                  checked={value[group.key].includes(row.id)}
                  disabled={
                    !value[group.key].includes(row.id) &&
                    value[group.key].length >= 8
                  }
                  onChange={(e) => toggle(group.key, row.id, e.target.checked)}
                />
                {row.name}
              </label>
            ))}
            {value[group.key]
              .filter(
                (id) => group.rows && !group.rows.some((row) => row.id === id),
              )
              .map((id) => (
                <label key={id} className="flex gap-3 text-base">
                  <input
                    type="checkbox"
                    checked
                    onChange={() => toggle(group.key, id, false)}
                  />
                  不可用资源 {id}
                </label>
              ))}
            {group.rows?.length === 0 && (
              <p className="text-base text-fg-muted">尚未添加{group.label}</p>
            )}
          </div>
        </section>
      ))}
      <ErrorNotice
        error={providers.error || models.error || skills.error || mcps.error}
      />
    </>
  );
}
