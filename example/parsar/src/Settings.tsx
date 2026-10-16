import { tr, setLanguage, currentLanguage } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { product } from "./lib/product";
import { PageHeader, ErrorNotice, Field } from "./components/shared";
import { Input } from "./components/ui/input";
import { Button } from "./components/ui/button";
export interface WorkspaceSettings {
  name: string;
  revision: number;
}
export function Settings() {
  const query = useQuery({
    queryKey: ["settings"],
    queryFn: () => product<WorkspaceSettings>("settings"),
  });
  return (
    <>
      <PageHeader title={tr("设置")} />
      <div className="flex items-center gap-3 border-b px-6 py-4">
        <span>{tr("界面语言")}</span>
        <Button
          variant={currentLanguage === "zh" ? "default" : "outline"}
          onClick={() => setLanguage("zh")}
        >
          简体中文
        </Button>
        <Button
          variant={currentLanguage === "en" ? "default" : "outline"}
          onClick={() => setLanguage("en")}
        >
          English
        </Button>
      </div>
      <ErrorNotice error={query.error} />
      {query.data && (
        <SettingsForm key={query.data.revision} value={query.data} />
      )}
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
    <form
      className="max-w-xl space-y-5 px-6 py-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <h2 className="text-lg font-medium">{tr("工作空间")}</h2>
      <Field label={tr("名称")} id="workspace-name">
        <Input
          id="workspace-name"
          value={name}
          required
          maxLength={80}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <ErrorNotice error={save.error} />
      <Button disabled={save.isPending} type="submit">
        {tr("保存设置")}
      </Button>
    </form>
  );
}
