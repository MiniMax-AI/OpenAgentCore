import { botFields } from "../shared/bot-bindings.mjs";
import { Select, SelectOption } from "./components/ui/select";
import { useState } from "react";
import { tr } from "./lib/i18n";
import type { RuntimeProfile } from "./lib/product";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Button } from "./components/ui/button";
import { Field, Toggle } from "./components/shared";

function Group({
  title,
  action,
  children,
}: {
  title: string;
  action: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-3 border-t border-line-muted pt-5">
      <div className="flex items-center justify-between gap-4">
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}

export function EnvironmentFields({
  value,
  onChange,
}: {
  value: RuntimeProfile;
  onChange: (value: RuntimeProfile) => void;
}) {
  const [savedNames] = useState(
    () =>
      new Set(
        Object.entries(value.env || {})
          .filter(([, entry]) => entry === null)
          .map(([name]) => name),
      ),
  );
  const env = Object.entries(value.env || {});
  const files = value.files || [];
  return (
    <div className="space-y-5">
      <Field label={tr("用途说明")} id="environment-description">
        <Input
          id="environment-description"
          value={value.description || ""}
          onChange={(e) => onChange({ ...value, description: e.target.value })}
          placeholder={tr("例如：飞书办公，预装 lark-cli")}
        />
      </Field>
      <Toggle
        label={tr("提前准备，新对话无需等待安装")}
        checked={!!value.prepare}
        onChange={(prepare) => onChange({ ...value, prepare })}
      />
      <div className="grid grid-cols-2 gap-4">
        {(["npm", "python"] as const).map((manager) => (
          <Field
            key={manager}
            label={manager === "npm" ? tr("npm 软件包") : tr("Python 软件包")}
            id={`environment-${manager}`}
          >
            <Textarea
              id={`environment-${manager}`}
              rows={4}
              value={(value.packages?.[manager] || []).join("\n")}
              placeholder={tr("每行一个，例如 {{value0}}", {
                value0: manager === "npm" ? "typescript@5.8.3" : "pandas==2.3.3",
              })}
              onChange={(e) =>
                onChange({
                  ...value,
                  packages: {
                    ...value.packages,
                    [manager]: e.target.value.split("\n"),
                  },
                })
              }
            />
          </Field>
        ))}
      </div>
      <Field label={tr("初始化脚本")} id="environment-setup">
        <Textarea
          id="environment-setup"
          rows={8}
          className="font-mono text-sm"
          value={value.setup_script || ""}
          onChange={(e) => onChange({ ...value, setup_script: e.target.value })}
          placeholder={tr(
            "# 示例：每个新对话开始前执行\nset -eu\nmkdir -p outputs",
          )}
        />
      </Field>
      <Field label={tr("网络")} id="environment-network">
        <Select
          id="environment-network"
          value={value.network?.access ?? "enabled"}
          onValueChange={(access) =>
            onChange({
              ...value,
              network:
                access === "enabled"
                  ? undefined
                  : access === "disabled"
                    ? { access }
                    : { access: "restricted", allowed_domains: [] },
            })
          }
        >
          <SelectOption value="enabled">{tr("允许联网")}</SelectOption>
          <SelectOption value="disabled">{tr("禁止联网")}</SelectOption>
          <SelectOption value="restricted">{tr("仅允许指定域名")}</SelectOption>
        </Select>
      </Field>
      {value.network?.access === "restricted" && (
        <Field label={tr("允许的域名")} id="environment-domains">
          <Textarea
            id="environment-domains"
            rows={4}
            required
            value={(value.network.allowed_domains || []).join("\n")}
            placeholder={tr("每行一个，例如 {{value0}}", {
              value0: "api.example.com",
            })}
            onChange={(e) => {
              const domains = e.target.value.split("\n");
              e.target.setCustomValidity(
                domains.some((d) => d.includes("*"))
                  ? tr("域名需完整填写，不支持通配符")
                  : domains.filter((d) => d.trim()).length > 100
                    ? tr("最多 100 个域名")
                    : "",
              );
              onChange({
                ...value,
                network: { access: "restricted", allowed_domains: domains },
              });
            }}
          />
        </Field>
      )}
      <Group
        title={tr("环境变量")}
        action={
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              let name = "VARIABLE";
              while (name in (value.env || {})) name += "_";
              onChange({ ...value, env: { ...value.env, [name]: "" } });
            }}
          >
            {tr("添加变量")}
          </Button>
        }
      >
        {env.map(([name, entry], i) => (
          <div key={i} className="flex gap-2">
            <Input
              aria-label={tr("变量名称")}
              value={name}
              onChange={(e) => {
                if (
                  e.target.value !== name &&
                  e.target.value in (value.env || {})
                ) {
                  e.target.setCustomValidity(tr("变量名称不能重复"));
                  return;
                }
                e.target.setCustomValidity("");
                const rows = [...env];
                rows[i] = [e.target.value, entry === null ? "" : entry];
                onChange({ ...value, env: Object.fromEntries(rows) });
              }}
            />
            <Input
              aria-label={tr("变量值")}
              type="password"
              autoComplete="new-password"
              value={entry || ""}
              placeholder={
                entry === null ? tr("已配置，留空保留") : tr("填写值")
              }
              onChange={(e) =>
                onChange({
                  ...value,
                  env: {
                    ...value.env,
                    [name]:
                      e.target.value || (savedNames.has(name) ? null : ""),
                  },
                })
              }
            />
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                onChange({
                  ...value,
                  env: Object.fromEntries(
                    env.filter((_, index) => index !== i),
                  ),
                })
              }
            >
              {tr("移除")}
            </Button>
          </div>
        ))}
      </Group>
      <Group
        title={tr("飞书机器人变量")}
        action={
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              let name = "FEISHU_APP_ID";
              while (name in (value.bot_env || {}) || name in (value.env || {}))
                name += "_";
              onChange({
                ...value,
                bot_env: { ...value.bot_env, [name]: "app_id" },
              });
            }}
          >
            {tr("添加机器人变量")}
          </Button>
        }
      >
        {Object.entries(value.bot_env || {}).map(
          ([name, source], index, rows) => (
            <div key={index} className="flex gap-2">
              <Input
                aria-label={tr("机器人变量名称")}
                value={name}
                onChange={(event) => {
                  if (
                    event.target.value !== name &&
                    event.target.value in (value.bot_env || {})
                  ) {
                    event.target.setCustomValidity(tr("变量名称不能重复"));
                    return;
                  }
                  event.target.setCustomValidity("");
                  const updated = [...rows];
                  updated[index] = [event.target.value, source];
                  onChange({
                    ...value,
                    bot_env: Object.fromEntries(updated),
                  });
                }}
              />
              <Select
                aria-label={tr("机器人字段")}
                value={source}
                onValueChange={(field) =>
                  onChange({
                    ...value,
                    bot_env: { ...value.bot_env, [name]: field },
                  })
                }
              >
                {Object.entries(botFields).map(([field, label]) => (
                  <SelectOption key={field} value={field}>
                    {tr(label)}
                  </SelectOption>
                ))}
              </Select>
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  onChange({
                    ...value,
                    bot_env: Object.fromEntries(
                      rows.filter((_, i) => i !== index),
                    ),
                  })
                }
              >
                {tr("移除")}
              </Button>
            </div>
          ),
        )}
      </Group>
      <Group
        title={tr("初始文件")}
        action={
          <Button
            type="button"
            variant="outline"
            onClick={() =>
              onChange({
                ...value,
                files: [...files, { path: "/workspace/", content: "" }],
              })
            }
          >
            {tr("添加文件")}
          </Button>
        }
      >
        {files.map((file, index) => (
          <div key={index} className="space-y-2 rounded-xl border border-line p-3.5">
            <div className="flex gap-2">
              <Input
                aria-label={tr("文件路径")}
                value={file.path}
                onChange={(e) =>
                  onChange({
                    ...value,
                    files: files.map((f, i) =>
                      i === index ? { ...f, path: e.target.value } : f,
                    ),
                  })
                }
              />
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  onChange({
                    ...value,
                    files: files.filter((_, i) => i !== index),
                  })
                }
              >
                {tr("移除")}
              </Button>
            </div>
            <Textarea
              aria-label={tr("文件内容")}
              rows={4}
              className="font-mono text-sm"
              value={file.content}
              onChange={(e) =>
                onChange({
                  ...value,
                  files: files.map((f, i) =>
                    i === index ? { ...f, content: e.target.value } : f,
                  ),
                })
              }
            />
          </div>
        ))}
      </Group>
    </div>
  );
}

export function environmentSummary(value: RuntimeProfile) {
  const packages = Object.values(value.packages || {}).flat().length;
  return (
    [
      packages ? tr("{{count}} 个软件包", { count: packages }) : "",
      value.setup_script ? tr("初始化脚本") : "",
      value.network ? tr("限制联网") : "",
      Object.keys(value.env || {}).length ? tr("环境变量") : "",
      value.files?.length ? tr("初始文件") : "",
      Object.keys(value.bot_env || {}).length ? tr("飞书机器人身份") : "",
    ]
      .filter(Boolean)
      .join(" · ") || tr("空白工作环境")
  );
}
