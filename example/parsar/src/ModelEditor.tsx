import { ProviderEditor } from "./ProviderEditor";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { product, useProviders, type ModelProfile } from "./lib/product";
import { Button } from "./components/ui/button";
import { Select, SelectOption } from "./components/ui/select";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { Field, ErrorNotice } from "./components/shared";
export function ModelEditor({
  value,
  close,
  onSaved,
}: {
  onSaved?: (id: string) => void;
  value: ModelProfile;
  close: () => void;
}) {
  const [form, setForm] = useState(value);
  const [providerId, setProviderId] = useState("");
  const cache = useQueryClient();
  const providers = useProviders();
  const save = useMutation({
    mutationFn: () => product(`models/${form.id}`, "PUT", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["models"] });
      void cache.invalidateQueries({ queryKey: ["providers"] });
      onSaved?.(form.id);
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
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {value.revision ? tr("编辑模型") : tr("添加模型")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="model-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            e.stopPropagation();
            save.mutate();
          }}
        >
          <Field label="Provider" id="model-provider">
            <Select
              id="model-provider"
              value={form.provider_id}
              onValueChange={(provider_id) => setForm({ ...form, provider_id })}
            >
              <SelectOption value="">{tr("选择 Provider")}</SelectOption>
              {providers.data?.map((provider) => (
                <SelectOption key={provider.id} value={provider.id}>
                  {provider.name}
                </SelectOption>
              ))}
            </Select>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setProviderId(crypto.randomUUID())}
            >
              {tr("添加 Provider")}
            </Button>
          </Field>
          <Field label={tr("显示名称")} id="model-name">
            <Input
              id="model-name"
              required
              value={form.name}
              maxLength={80}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("模型 ID")} id="model-id">
            <Input
              id="model-id"
              required
              value={form.model}
              maxLength={200}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
              placeholder={tr("例如 kimi-k2.6")}
            />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.enabled !== false}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            {tr("启用模型")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={!!form.pricing}
              onChange={(e) =>
                setForm({
                  ...form,
                  pricing: e.target.checked
                    ? { input: 0, output: 0 }
                    : undefined,
                })
              }
            />
            {tr("设置估算价格（美元 / 百万 Token）")}
          </label>
          {form.pricing && (
            <div className="grid grid-cols-2 gap-3">
              {(["input", "output"] as const).map((key) => (
                <Field
                  key={key}
                  id={`price-${key}`}
                  label={key === "input" ? tr("输入价格") : tr("输出价格")}
                >
                  <Input
                    id={`price-${key}`}
                    type="number"
                    min="0"
                    step="any"
                    required
                    value={form.pricing![key]}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        pricing: {
                          ...form.pricing!,
                          [key]: Number(e.target.value),
                        },
                      })
                    }
                  />
                </Field>
              ))}
            </div>
          )}
          <ErrorNotice error={save.error} />
        </form>
        {providerId && (
          <ProviderEditor
            value={{ id: providerId, name: "", revision: 0 }}
            close={() => setProviderId("")}
            onSaved={(provider_id) => setForm({ ...form, provider_id })}
          />
        )}
        <DialogFooter>
          <Button
            type="submit"
            form="model-form"
            disabled={save.isPending || !form.provider_id}
          >
            {tr("保存")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
