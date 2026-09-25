import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";

export interface ConsoleSelectOption {
  value: string;
  label: string;
}

/**
 * The console's select: coss ui's Base UI select, sized to the 30px control
 * row. On the canvas-free page panel it is a ringed white control; its popup is
 * a hairline card with a checked row, keyboard navigation and typeahead.
 */
export function ConsoleSelect({
  value,
  onChange,
  options,
  label,
  className,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  options: readonly ConsoleSelectOption[];
  /** Accessible name; the trigger shows the selected option's label. */
  label: string;
  className?: string;
  disabled?: boolean;
}) {
  return (
    <Select
      value={value}
      onValueChange={(next) => onChange(typeof next === "string" ? next : "")}
      items={options}
      disabled={disabled}
    >
      {/* The trigger is the combobox; Base UI's root renders no element to name. */}
      <SelectTrigger
        aria-label={label}
        size="sm"
        className={cn(
          "h-[30px] min-h-[30px] w-auto min-w-36 max-w-60 rounded-[8px] border-0 bg-surface text-[13px] text-ink shadow-btn sm:min-h-[30px] sm:text-[13px]",
          "hover:bg-inset focus-visible:shadow-[0_0_0_1px_var(--accent),0_0_0_4px_var(--accent-tint)] focus-visible:ring-0",
          className,
        )}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectPopup className="text-[13px]" alignItemWithTrigger={false} sideOffset={6}>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value} className="text-[13px] sm:text-[13px]">
            {option.label}
          </SelectItem>
        ))}
      </SelectPopup>
    </Select>
  );
}
