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
import { ErrorNotice } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { randomUuid } from "@oac/agents-client";
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
      <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-3 px-6 pt-6 pb-1 sm:px-10">
        <h2 className="text-lg font-semibold tracking-tight text-fg">{tr("模型")}</h2>
        <div className="flex items-center gap-2">
          <Button
            onClick={() =>
              setProvider({ id: randomUuid(), name: "", revision: 0 })
            }
          >
            <Plus />
            {tr("添加 Provider")}
          </Button>
        </div>
      </div>
      <ErrorNotice page error={models.error || providers.error || remove.error} />
      <div className="mx-auto w-full max-w-6xl space-y-5 px-6 py-5 sm:px-10">
        {providers.data?.map((group) => (
          <section
            key={group.id}
            aria-label={group.name}
            className="overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card"
          >
            <div className="flex min-h-12 flex-wrap items-center gap-3 border-b border-line bg-surface-subtle/80 px-5 py-2.5">
              <h2 className="min-w-0 flex-1 break-words text-sm font-semibold text-fg">
                {group.name}
              </h2>
              <span className="rounded-md bg-surface px-2 py-0.5 text-sm text-fg-muted ring-1 ring-line">
                {models.data?.filter((model) => model.provider_id === group.id)
                  .length || 0}{" "}
                {tr("个模型")}
              </span>
              <Button
                variant="outline"
                size="sm"
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
                    className="flex min-h-12 items-center gap-3 px-5 py-2.5 transition-colors hover:bg-surface-hover/50"
                  >
                    <div className="min-w-0 flex-1">
                      <h3 className="truncate text-sm font-medium text-fg">
                        {model.name}
                      </h3>
                      {model.name !== model.model && (
                        <p className="mt-0.5 break-words font-mono text-sm text-fg-muted">
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
