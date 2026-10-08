"use client";

// The console's searchable model selector, in the same coss ui vocabulary as
// `console-select.tsx`: a 30px ringed control whose popup is a hairline card.
// Base UI's combobox already provides the search input, keyboard navigation and
// typeahead; this file only binds it to the console's tokens.

import { Combobox } from "@base-ui/react/combobox";
import { ChevronDownIcon } from "lucide-react";
import { useMemo, useRef } from "react";

import { cn } from "@/lib/utils";

export interface ModelOption {
  /** The exact model identifier, kept verbatim. */
  value: string;
  /** What the row shows. */
  label: string;
}

/**
 * A searchable list bound to the options the current entrance admits. Typing
 * filters the list; it never becomes the value, so an operator cannot save a
 * model the catalog did not offer.
 */
export function ModelCombobox({
  value,
  onChange,
  options,
  label,
  placeholder,
  disabled,
  empty,
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  options: readonly ModelOption[];
  /** Accessible name for the input and the list. */
  label: string;
  placeholder?: string;
  disabled?: boolean;
  /** Shown when the search matches nothing. */
  empty: string;
  className?: string;
}) {
  const anchor = useRef<HTMLDivElement>(null);
  const items = useMemo(() => options.map((option) => ({ value: option.value, label: option.label })), [options]);
  const selected = items.find((option) => option.value === value);
  return (
    <Combobox.Root
      items={items}
      value={value === "" ? null : value}
      onValueChange={(next) => onChange(typeof next === "string" ? next : "")}
      itemToStringLabel={(itemValue) => items.find((option) => option.value === itemValue)?.label ?? itemValue}
      disabled={disabled}
    >
      <div ref={anchor} className={cn("relative", className)}>
        <Combobox.InputGroup
          data-model-control="true"
          className={cn(
            "relative flex h-[30px] min-h-[30px] w-full items-center rounded-[8px] bg-surface text-[13px] text-ink shadow-btn",
            "focus-within:shadow-[0_0_0_1px_var(--accent),0_0_0_4px_var(--accent-tint)]",
            "data-disabled:opacity-64",
          )}
        >
          <Combobox.Input
            aria-label={label}
            placeholder={selected?.label ?? placeholder}
            spellCheck={false}
            autoComplete="off"
            className="h-full w-full min-w-0 flex-1 truncate bg-transparent px-3 text-[13px] text-ink outline-none placeholder:text-ink-3"
          />
          <Combobox.Trigger className="flex h-full w-7 shrink-0 items-center justify-center text-ink-2" aria-label={label}>
            <ChevronDownIcon size={16} aria-hidden="true" />
          </Combobox.Trigger>
        </Combobox.InputGroup>
        <Combobox.Portal>
          <Combobox.Positioner sideOffset={6} align="start" anchor={anchor} className="z-50">
            <Combobox.Popup
              data-model-options="true"
              className="w-[var(--anchor-width)] min-w-[22rem] max-w-[min(34rem,var(--available-width))] rounded-[12px] border border-line bg-surface p-1 text-ink shadow-(--shadow-overlay)"
            >
              <Combobox.Empty className="px-3 py-2 text-[13px] text-ink-3">{empty}</Combobox.Empty>
              <Combobox.List className="max-h-[min(22rem,var(--available-height))] overflow-y-auto overscroll-contain">
                {(item: ModelOption) => (
                  <Combobox.Item
                    key={item.value}
                    value={item.value}
                    className="grid min-h-8 cursor-default grid-cols-[1rem_minmax(0,1fr)] items-baseline gap-2 rounded-[6px] px-2 py-1 text-[13px] outline-none data-highlighted:bg-hover-2"
                  >
                    <Combobox.ItemIndicator className="col-start-1 self-center text-accent">
                      <svg aria-hidden="true" fill="none" height="14" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" viewBox="0 0 24 24" width="14">
                        <path d="M5.252 12.7 10.2 18.63 18.748 5.37" />
                      </svg>
                    </Combobox.ItemIndicator>
                    <span className="col-start-2 min-w-0">
                      <span className="block truncate">{item.label}</span>
                      <span className="block truncate font-mono text-[11px] text-ink-3" data-model-id={item.value}>{item.value}</span>
                    </span>
                  </Combobox.Item>
                )}
              </Combobox.List>
            </Combobox.Popup>
          </Combobox.Positioner>
        </Combobox.Portal>
      </div>
    </Combobox.Root>
  );
}
