import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Pencil, Trash2 } from "lucide-react";
import {
  product,
  useModels,
  useProviders,
  type ModelProfile,
  type ProviderProfile,
} from "./lib/product";
import { ModelEditor } from "./ModelEditor";
import { Button } from "./components/ui/button";
import { ProviderEditor } from "./ProviderEditor";
import { Help, ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
export function Models() {
  const models = useModels();
  const providers = useProviders();
  const cache = useQueryClient();
  const [editing, setEditing] = useState<ModelProfile | null>(null);
  const [provider, setProvider] = useState<ProviderProfile | null>(null);
  const remove = useMutation({
    mutationFn: (path: string) => product(path, "DELETE"),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["models"] });
      void cache.invalidateQueries({ queryKey: ["providers"] });
    },
  });
  return (
    <>
      <PageHeader title={tr("模型")}>
        <Help>
          {tr(
            "获取 Provider 的模型列表，勾选或自定义后保存。Agent 选择已绑定的模型。",
          )}
        </Help>
        <Button
          onClick={() =>
            setProvider({ id: crypto.randomUUID(), name: "", revision: 0 })
          }
        >
          <Plus />
          {tr("添加 Provider")}
        </Button>
      </PageHeader>
      <ErrorNotice error={models.error || providers.error || remove.error} />
      <div className="min-h-0 flex-1 overflow-auto">
        {providers.data?.map((group) => (
          <section
            key={group.id}
            aria-label={group.name}
            className="border-b border-line"
          >
            <div className="flex min-h-9 flex-wrap items-center gap-2 border-b border-line bg-surface-subtle px-6">
              <h2 className="min-w-0 flex-1 break-words text-sm font-medium">
                {group.name}
              </h2>
              <span className="text-xs text-fg-muted">
                {models.data?.filter((model) => model.provider_id === group.id)
                  .length || 0}{" "}
                {tr("个模型")}
              </span>
              <Button
                variant="ghost"
                aria-label={tr("编辑 Provider {{value0}}", {
                  value0: group.name,
                })}
                disabled={!models.data}
                onClick={() => setProvider(group)}
              >
                {tr("管理模型")}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("移除 Provider {{value0}}", {
                  value0: group.name,
                })}
                disabled={remove.isPending}
                onClick={() => {
                  if (
                    confirm(
                      tr("移除 Provider {{value0}}？请先移除其中的模型。", {
                        value0: group.name,
                      }),
                    )
                  )
                    remove.mutate(`providers/${group.id}`);
                }}
              >
                <Trash2 />
              </Button>
            </div>
            <div className="divide-y divide-line-muted">
              {models.data
                ?.filter((model) => model.provider_id === group.id)
                .map((model) => (
                  <div
                    key={model.id}
                    className="flex min-h-9 items-center gap-3 px-6"
                  >
                    <div className="min-w-0 flex-1">
                      <h3 className="truncate text-sm font-medium">
                        {model.name}
                      </h3>
                      {model.name !== model.model && (
                        <p className="break-words text-xs text-fg-muted">
                          {model.model}
                        </p>
                      )}
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={tr("编辑 {{value0}}", { value0: model.name })}
                      onClick={() => setEditing(model)}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={tr("移除 {{value0}}", { value0: model.name })}
                      disabled={remove.isPending}
                      onClick={() => {
                        if (
                          confirm(
                            tr("移除模型 {{value0}}？", { value0: model.name }),
                          )
                        )
                          remove.mutate(`models/${model.id}`);
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                ))}
            </div>
          </section>
        ))}
        {!providers.isPending && !providers.data?.length && (
          <EmptyState title={tr("添加 Provider，选择需要的模型")} />
        )}
      </div>
      {editing && (
        <ModelEditor value={editing} close={() => setEditing(null)} />
      )}
      {provider && (
        <ProviderEditor value={provider} close={() => setProvider(null)} />
      )}
    </>
  );
}
