import { tr, setLanguage, currentLanguage } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { product } from "./lib/product";
import { setTheme, useTheme, type ThemeChoice } from "./lib/theme";
import { PageHeader, ErrorNotice, Field, Section } from "./components/shared";
import { Input } from "./components/ui/input";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";
export interface WorkspaceSettings {
  name: string;
  revision: number;
}
function Choice<T extends string>({
  label,
  value,
  options,
  change,
}: {
  label: string;
  value: T;
  options: [T, string][];
  change: (value: T) => void;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4">
      <span className="text-sm text-fg">{label}</span>
      <div
        role="radiogroup"
        aria-label={label}
        className="inline-flex rounded-[10px] bg-surface-muted p-1"
      >
        {options.map(([key, text]) => (
          <button
            key={key}
            role="radio"
            aria-checked={value === key}
            onClick={() => change(key)}
            className={cn(
              "h-8 rounded-lg px-4 text-sm transition-colors",
              value === key
                ? "bg-surface font-medium text-fg app-shadow-control"
                : "text-fg-muted hover:text-fg",
            )}
          >
            {text}
          </button>
        ))}
      </div>
    </div>
  );
}
export function Settings() {
  const query = useQuery({
    queryKey: ["settings"],
    queryFn: () => product<WorkspaceSettings>("settings"),
  });
  const { choice } = useTheme();
  return (
    <>
      <PageHeader title={tr("设置")} narrow />
      <ErrorNotice page error={query.error} className="max-w-3xl" />
      <div className="mx-auto w-full max-w-3xl space-y-6 px-6 pb-12 sm:px-10">
        {query.data && (
          <SettingsForm key={query.data.revision} value={query.data} />
        )}
        <Section title={tr("外观")}>
          <Choice<"zh" | "en">
            label={tr("界面语言")}
            value={currentLanguage === "en" ? "en" : "zh"}
            options={[
              ["zh", "简体中文"],
              ["en", "English"],
            ]}
            change={(language) => {
              if (language !== currentLanguage) setLanguage(language);
            }}
          />
          <Choice<ThemeChoice>
            label={tr("主题")}
            value={choice}
            options={[
              ["light", tr("浅色")],
              ["dark", tr("深色")],
              ["system", tr("跟随系统")],
            ]}
            change={setTheme}
          />
        </Section>
        <Section title={tr("记录")}>
          <div className="flex flex-wrap gap-2.5">
            {[
              ["/runs", tr("运行")],
              ["/usage", tr("用量")],
              ["/audit", tr("审计日志")],
            ].map(([href, label]) => (
              <Button key={href} asChild variant="outline">
                <a href={`#${href}`}>{label}</a>
              </Button>
            ))}
          </div>
        </Section>
      </div>
    </>
  );
}
function SettingsForm({ value }: { value: WorkspaceSettings }) {
  const [name, setName] = useState(value.name),
    cache = useQueryClient();
  const save = useMutation({
    mutationFn: () => product("settings", "PUT", { ...value, name }),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["settings"] }),
  });
  return (
    <Section title={tr("工作空间")}>
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="min-w-56 flex-1">
          <Field label={tr("名称")} id="workspace-name">
            <Input
              id="workspace-name"
              value={name}
              required
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
        </div>
        <Button
          disabled={save.isPending || name === value.name}
          type="submit"
        >
          {tr("保存设置")}
        </Button>
      </form>
      <ErrorNotice error={save.error} className="my-0" />
    </Section>
  );
}
