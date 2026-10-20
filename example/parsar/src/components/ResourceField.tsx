import { cn } from "../lib/utils";
import { useId, useState, type ReactNode } from "react";
import { ChevronDown, Plus, Search, Settings2, X } from "lucide-react";
import { tr } from "../lib/i18n";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./ui/dialog";

export interface ResourceOption {
  id: string;
  name: string;
  description?: string;
  disabled?: boolean;
}

// One binding field for single resources and ordered collections of resources.
export function ResourceField({
  label,
  options,
  selected,
  change,
  multiple = false,
  limit,
  description,
  empty,
  loading,
  create,
  createLabel,
  inspect,
  detail,
  manage,
  required,
  error,
}: {
  label: string;
  options: ResourceOption[];
  selected: string[];
  change: (ids: string[]) => void;
  multiple?: boolean;
  limit?: number;
  description?: string;
  empty?: string;
  loading?: boolean;
  create?: () => void;
  createLabel?: string;
  inspect?: (id: string) => void;
  detail?: (id: string) => ReactNode;
  manage?: { href: string; label: string };
  required?: boolean;
  /** Shown under the field after a failed save attempt. */
  error?: string;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const rows = options.filter((row) =>
    `${row.name} ${row.description || ""}`
      .toLocaleLowerCase()
      .includes(query.toLocaleLowerCase().trim()),
  );
  const name = (key: string) =>
    options.find((row) => row.id === key)?.name ||
    (loading ? tr("加载中…") : `${tr("不可用资源")} · ${key}`);
  return (
    <section
      className="space-y-2"
      aria-label={label}
      data-invalid={error ? "true" : undefined}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex items-baseline text-sm font-medium">
          <label htmlFor={id}>{label}</label>
          {required && (
            <span aria-hidden className="ml-1 text-fg-subtle">
              *
            </span>
          )}
        </span>
        {multiple && (
          <span className="text-sm tabular-nums text-fg-subtle">
            {selected.length}
            {limit !== undefined ? ` / ${limit}` : ""}
          </span>
        )}
      </div>
      {description && (
        <p className="text-sm leading-relaxed text-fg-muted">{description}</p>
      )}
      <div className="flex gap-2">
        <Button
          id={id}
          type="button"
          variant="outline"
          className={cn(
            "min-w-0 flex-1 justify-between font-normal text-fg-muted",
            error && "border-danger-border",
          )}
          aria-haspopup="dialog"
          aria-invalid={error ? true : undefined}
          onClick={() => {
            setQuery("");
            setOpen(true);
          }}
        >
          <span className="truncate">
            {!multiple && selected.length
              ? tr("更换选择")
              : empty || `${tr("选择")} ${label}`}
          </span>
          <ChevronDown className="h-3.5 w-3.5 shrink-0" />
        </Button>
        {create && (
          <Button
            type="button"
            variant="outline"
            className="shrink-0"
            aria-label={createLabel}
            disabled={limit !== undefined && selected.length >= limit}
            onClick={create}
          >
            <Plus className="h-3.5 w-3.5" />
            {tr("新建")}
          </Button>
        )}
        {manage && (
          <Button asChild variant="outline" className="shrink-0">
            <a href={manage.href}>
              <Settings2 className="h-3.5 w-3.5" />
              {manage.label}
            </a>
          </Button>
        )}
      </div>
      {error && <p className="text-sm text-danger">{error}</p>}
      {!!selected.length && (
        <div className="divide-y divide-line-muted rounded-xl border border-line">
          {selected.map((key) => (
            <div
              key={key}
              className="flex flex-wrap items-center gap-2 py-1.5 pl-4 pr-1.5"
            >
              <div className="min-w-0 flex-1">
                <span className="block truncate text-sm">{name(key)}</span>
                {options.find((row) => row.id === key)?.disabled && (
                  <span className="text-sm text-danger">{tr("已停用")}</span>
                )}
              </div>
              {detail?.(key)}
              {inspect && options.some((row) => row.id === key) && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={tr("查看或编辑 {{name}}", { name: name(key) })}
                  title={tr("查看或编辑 {{name}}", { name: name(key) })}
                  onClick={() => inspect(key)}
                >
                  <Settings2 className="h-3.5 w-3.5" />
                </Button>
              )}
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={tr("移除绑定 {{name}}", { name: name(key) })}
                title={tr("移除绑定 {{name}}", { name: name(key) })}
                onClick={() =>
                  change(selected.filter((value) => value !== key))
                }
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          aria-describedby={undefined}
          className="max-w-[calc(100vw-2rem)] sm:max-w-lg"
        >
          <DialogHeader>
            <DialogTitle>{tr("选择{{name}}", { name: label })}</DialogTitle>
          </DialogHeader>
          <div className="relative">
            <Search className="absolute left-2.5 top-2 h-4 w-4 text-fg-muted" />
            <Input
              className="pl-9"
              aria-label={tr("搜索{{name}}", { name: label })}
              placeholder={tr("按名称或说明搜索")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <div
            className="max-h-[45dvh] space-y-1 overflow-y-auto"
            role="group"
            aria-label={label}
          >
            {!multiple && (
              <label className="flex cursor-pointer items-center gap-3 rounded-xl p-3 text-sm hover:bg-surface-muted">
                <input
                  type="radio"
                  name={id}
                  checked={!selected.length}
                  onChange={() => {
                    change([]);
                    setOpen(false);
                  }}
                />
                {empty || tr("不选择")}
              </label>
            )}
            {rows.map((row) => (
              <label
                key={row.id}
                className="flex cursor-pointer items-start gap-3 rounded-xl border border-transparent p-3 hover:bg-surface-muted has-[:checked]:border-line has-[:checked]:bg-surface-muted has-[:disabled]:opacity-50"
              >
                <input
                  type={multiple ? "checkbox" : "radio"}
                  name={id}
                  className="mt-1 accent-accent"
                  aria-label={row.name}
                  checked={selected.includes(row.id)}
                  disabled={
                    !selected.includes(row.id) &&
                    (row.disabled ||
                      (limit !== undefined && selected.length >= limit))
                  }
                  onChange={(event) => {
                    change(
                      multiple
                        ? event.target.checked
                          ? [...selected, row.id]
                          : selected.filter((key) => key !== row.id)
                        : [row.id],
                    );
                    if (!multiple) setOpen(false);
                  }}
                />
                <span className="min-w-0">
                  <span className="block break-words text-sm font-medium">
                    {row.name}
                  </span>
                  {row.description && (
                    <span className="mt-1 block break-words text-sm text-fg-muted">
                      {row.description}
                    </span>
                  )}
                  {row.disabled && (
                    <span className="text-sm text-danger">{tr("已停用")}</span>
                  )}
                </span>
              </label>
            ))}
            {!rows.length && (
              <p className="py-8 text-center text-sm text-fg-muted">
                {loading
                  ? tr("加载中…")
                  : query
                    ? tr("没有匹配的资源")
                    : tr("暂无可选资源")}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" onClick={() => setOpen(false)}>
              {tr("完成")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
