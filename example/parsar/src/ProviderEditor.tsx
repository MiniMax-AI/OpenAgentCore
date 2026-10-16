import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  product,
  type ModelProfile,
  type ProviderProfile,
} from "./lib/product";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { Field, Help, ErrorNotice } from "./components/shared";
type Choice = {
  model: string;
  name: string;
};
export function ProviderEditor({
  value,
  close,
  onSaved,
}: {
  onSaved?: (id: string) => void;
  value: ProviderProfile;
  close: () => void;
}) {
  const snapshot = useQuery({
    queryKey: ["provider-editor", value.id],
    queryFn: () =>
      product<
        ProviderProfile & {
          models: ModelProfile[];
        }
      >(`providers/${value.id}`),
    enabled: Boolean(value.revision),
    gcTime: 0,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  if (!value.revision)
    return (
      <ProviderForm value={value} models={[]} close={close} onSaved={onSaved} />
    );
  if (snapshot.data)
    return (
      <ProviderForm
        value={snapshot.data}
        models={snapshot.data.models}
        close={close}
        onSaved={onSaved}
      />
    );
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{tr("编辑 Provider")}</DialogTitle>
        </DialogHeader>
        {snapshot.isPending ? (
          <p className="text-base" role="status">
            {tr("加载中\u2026")}
          </p>
        ) : (
          <ErrorNotice
            error={snapshot.error}
            retry={() => void snapshot.refetch()}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
function ProviderForm({
  value,
  models,
  close,
  onSaved,
}: {
  onSaved?: (id: string) => void;
  value: ProviderProfile;
  models: ModelProfile[];
  close: () => void;
}) {
  const [name, setName] = useState(value.name);
  const [base, setBase] = useState(value.base_url || "");
  const [key, setKey] = useState("");
  const [step, setStep] = useState<"connection" | "models">("connection");
  const initial = models.filter((model) => model.provider_id === value.id);
  const [choices, setChoices] = useState<Choice[]>(initial);
  const [selected, setSelected] = useState(
    () => new Set(initial.map((row) => row.model)),
  );
  const [catalog, setCatalog] = useState<string[] | null>(null);
  const [search, setSearch] = useState("");
  const [custom, setCustom] = useState(false);
  const [customID, setCustomID] = useState("");
  const [customName, setCustomName] = useState("");
  const cache = useQueryClient();
  const connection = () => ({
    id: value.id,
    revision: value.revision,
    name: name.trim(),
    base_url: base.trim(),
    ...(key ? { api_key: key } : {}),
  });
  const discover = useMutation({
    mutationFn: () =>
      product<{
        models: string[];
      }>("providers/discover", "POST", connection()),
    onSuccess: ({ models: found }) => {
      setCatalog(found);
      setChoices((old) => [
        ...old,
        ...found
          .filter((id) => !old.some((row) => row.model === id))
          .map((model) => ({ model, name: model.slice(0, 80) })),
      ]);
      setStep("models");
    },
  });
  const save = useMutation({
    mutationFn: () =>
      product(`providers/${value.id}`, "PUT", {
        ...connection(),
        models: choices
          .filter((row) => selected.has(row.model))
          .map(({ model, name }) => ({ model, name })),
      }),
    onSuccess: async () => {
      await Promise.all([
        cache.invalidateQueries({ queryKey: ["providers"] }),
        cache.invalidateQueries({ queryKey: ["models"] }),
      ]);
      onSaved?.(value.id);
      close();
    },
  });
  const busy = discover.isPending || save.isPending;
  const toggle = (id: string, checked: boolean) =>
    setSelected((old) => {
      const next = new Set(old);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  const addCustom = () => {
    const model = customID.trim();
    if (!model) return;
    setChoices((old) =>
      old.some((row) => row.model === model)
        ? old
        : [...old, { model, name: customName.trim() || model.slice(0, 80) }],
    );
    toggle(model, true);
    setSearch("");
    setCustomID("");
    setCustomName("");
    setCustom(false);
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] max-w-xl overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>
            {step === "connection"
              ? value.revision
                ? tr("编辑 Provider")
                : tr("添加 Provider")
              : tr("{{value0}} \u00B7 选择模型", { value0: name })}
          </DialogTitle>
        </DialogHeader>
        {step === "connection" ? (
          <form
            id="provider-connect"
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              e.stopPropagation();
              discover.mutate();
            }}
          >
            <Field label={tr("Provider 名称")} id="provider-name">
              <Input
                id="provider-name"
                disabled={busy}
                required
                maxLength={80}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={tr("例如 MiniMax")}
              />
            </Field>
            <Field label="Base URL" id="provider-base">
              <Input
                id="provider-base"
                disabled={busy}
                value={base}
                maxLength={2000}
                onChange={(e) => setBase(e.target.value)}
                placeholder="https://api.example.com/v1"
              />
            </Field>
            <Field label="API Key" id="provider-key">
              <Input
                id="provider-key"
                disabled={busy}
                type="password"
                autoComplete="off"
                value={key}
                maxLength={8192}
                onChange={(e) => setKey(e.target.value)}
                placeholder={
                  value.has_api_key
                    ? tr("已保存，留空保持不变")
                    : tr("填写 API Key")
                }
              />
            </Field>
            <div className="flex items-center gap-2 text-base">
              <span>{tr("模型连接")}</span>
              <Help>
                {tr(
                  "地址和密钥用于获取模型列表。密钥仅保存在后端，不返回浏览器。Codex 和 Claude Code 使用此连接；MiniMax Code 使用部署配置的连接。",
                )}
              </Help>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            <div
              className="flex flex-wrap items-center justify-between gap-2 text-base"
              aria-live="polite"
            >
              <span>
                {catalog
                  ? tr("获取到 {{value0}} 个模型", { value0: catalog.length })
                  : tr("共 {{value0}} 个模型", { value0: choices.length })}{" "}
                {tr("· 已选 {{count}} 个", { count: selected.size })}
              </span>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => setCustom(!custom)}
              >
                {tr("自定义模型")}
              </Button>
            </div>
            {custom && (
              <div className="space-y-3 rounded-lg border border-line p-3">
                <Field label={tr("模型 ID")} id="custom-model">
                  <Input
                    id="custom-model"
                    value={customID}
                    maxLength={200}
                    onChange={(e) => setCustomID(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        e.stopPropagation();
                        addCustom();
                      }
                    }}
                  />
                </Field>
                <Field label={tr("显示名称（选填）")} id="custom-name">
                  <Input
                    id="custom-name"
                    value={customName}
                    maxLength={80}
                    onChange={(e) => setCustomName(e.target.value)}
                  />
                </Field>
                <Button
                  variant="outline"
                  onClick={addCustom}
                  disabled={!customID.trim() || busy}
                >
                  {tr("加入列表")}
                </Button>
              </div>
            )}
            <Input
              aria-label={tr("搜索模型")}
              placeholder={tr("搜索模型")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <div
              className="max-h-72 space-y-1 overflow-y-auto"
              aria-label={tr("可选模型")}
            >
              {choices
                .filter((row) =>
                  `${row.model} ${row.name}`
                    .toLowerCase()
                    .includes(search.toLowerCase()),
                )
                .map((row) => (
                  <label
                    key={row.model}
                    className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-3 text-base hover:bg-surface-hover"
                  >
                    <input
                      type="checkbox"
                      checked={selected.has(row.model)}
                      disabled={busy}
                      onChange={(e) => toggle(row.model, e.target.checked)}
                      className="h-4 w-4 shrink-0 accent-accent"
                    />
                    <span className="min-w-0 break-words">{row.model}</span>
                  </label>
                ))}
              {!choices.length && (
                <p className="py-4 text-base text-fg-muted">
                  {tr("没有模型，可以添加自定义模型。")}
                </p>
              )}
              {choices.length > 0 &&
                !choices.some((row) =>
                  `${row.model} ${row.name}`
                    .toLowerCase()
                    .includes(search.toLowerCase()),
                ) && (
                  <p className="py-4 text-base text-fg-muted">
                    {tr("没有匹配的模型。")}
                  </p>
                )}
            </div>
          </div>
        )}
        <ErrorNotice error={discover.error || save.error} />
        <DialogFooter>
          {step === "connection" ? (
            <>
              <Button
                variant="ghost"
                disabled={!name.trim() || busy}
                onClick={() => {
                  setStep("models");
                  discover.reset();
                }}
              >
                {tr("手动选择")}
              </Button>
              <Button
                type="submit"
                form="provider-connect"
                disabled={!name.trim() || !base.trim() || busy}
              >
                {discover.isPending ? tr("获取中\u2026") : tr("获取模型")}
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => setStep("connection")}
              >
                {tr("上一步")}
              </Button>
              <Button disabled={busy || custom} onClick={() => save.mutate()}>
                {save.isPending ? tr("保存中\u2026") : tr("保存")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
