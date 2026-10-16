import { connectionFields } from "../shared/connection-bindings.mjs";
import { Select, SelectOption } from "./components/ui/select";
import { useState } from "react";
import { tr } from "./lib/i18n";
import type { RuntimeProfile } from "./lib/product";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Button } from "./components/ui/button";
import { Field } from "./components/shared";

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
      <p className="text-xs text-fg-muted">
        {tr(
          "新会话会先准备此环境，再启动 Agent。修改模板只影响之后创建的会话。",
        )}
      </p>
      <Field label={tr("用途说明")} id="environment-description">
        <Input
          id="environment-description"
          value={value.description || ""}
          onChange={(e) => onChange({ ...value, description: e.target.value })}
          placeholder={tr("例如：飞书办公，预装 lark-cli")}
        />
      </Field>
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
              placeholder={
                manager === "npm" ? "typescript@5.8.3" : "pandas==2.3.3"
              }
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
      <p className="text-xs text-fg-muted">
        {tr(
          "每行一个软件包，可指定版本；在初始化脚本之前安装。系统软件由运行环境提供。",
        )}
      </p>
      <Field label={tr("初始化脚本")} id="environment-setup">
        <Textarea
          id="environment-setup"
          rows={8}
          className="font-mono text-xs"
          value={value.setup_script || ""}
          onChange={(e) => onChange({ ...value, setup_script: e.target.value })}
          placeholder={
            "set -eu\nmkdir -p outputs\n# Install tools or prepare project files"
          }
        />
      </Field>
      <p className="text-xs text-fg-muted">
        {tr(
          "Bash 脚本在工作目录执行一次；失败则停止环境准备。使用相对路径，不在脚本中填写密钥。",
        )}
      </p>
      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium">{tr("环境变量")}</h3>
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
        </div>
        <p className="text-xs text-fg-muted">
          {tr("变量值保存后不再显示；已有值留空保留。Agent 可以读取这些变量。")}
        </p>
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
      </section>
      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium">{tr("连接变量映射")}</h3>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              let name = "ACCOUNT_TOKEN";
              while (
                name in (value.connection_env || {}) ||
                name in (value.env || {})
              )
                name += "_";
              onChange({
                ...value,
                connection_env: {
                  ...value.connection_env,
                  [name]: "access_token",
                },
              });
            }}
          >
            {tr("添加连接变量")}
          </Button>
        </div>
        <p className="text-xs text-fg-muted">
          {tr(
            "Agent 选择账号连接；环境模板决定工具通过哪些变量读取连接。令牌在启动前注入，不保存在模板中。",
          )}
        </p>
        {Object.entries(value.connection_env || {}).map(
          ([name, source], index, rows) => (
            <div key={index} className="flex gap-2">
              <Input
                aria-label={tr("连接变量名称")}
                value={name}
                onChange={(event) => {
                  if (
                    event.target.value !== name &&
                    event.target.value in (value.connection_env || {})
                  ) {
                    event.target.setCustomValidity(tr("变量名称不能重复"));
                    return;
                  }
                  event.target.setCustomValidity("");
                  const updated = [...rows];
                  updated[index] = [event.target.value, source];
                  onChange({
                    ...value,
                    connection_env: Object.fromEntries(updated),
                  });
                }}
              />
              <Select
                aria-label={tr("连接字段")}
                value={source}
                onValueChange={(field) =>
                  onChange({
                    ...value,
                    connection_env: { ...value.connection_env, [name]: field },
                  })
                }
              >
                {Object.entries(connectionFields).map(([field, label]) => (
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
                    connection_env: Object.fromEntries(
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
      </section>
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium">{tr("初始文件")}</h3>
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
        </div>
        <p className="text-xs text-fg-muted">
          {tr("文本文件会在脚本执行前写入工作目录。路径以 /workspace/ 开头。")}
        </p>
        {files.map((file, index) => (
          <div key={index} className="space-y-2 rounded border border-line p-3">
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
              className="font-mono text-xs"
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
      </section>
    </div>
  );
}

export function environmentSummary(value: RuntimeProfile) {
  const packages = Object.values(value.packages || {}).flat().length;
  return (
    [
      packages ? tr("{{count}} 个软件包", { count: packages }) : "",
      value.setup_script ? tr("初始化脚本") : "",
      Object.keys(value.env || {}).length ? tr("环境变量") : "",
      value.files?.length ? tr("初始文件") : "",
      Object.keys(value.connection_env || {}).length ? tr("连接变量映射") : "",
    ]
      .filter(Boolean)
      .join(" · ") || tr("空白工作环境")
  );
}
