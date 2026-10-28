import * as React from "react";
import { cn } from "../../lib/utils";

/** Multi-line field styled like Input: paper, strong hairline, control shadow, indigo focus. */
export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => {
  return (
    <textarea
      ref={ref}
      className={cn(
        "app-shadow-control flex min-h-[80px] w-full rounded-[10px] border border-line-strong bg-surface px-3.5 py-2.5 text-sm leading-relaxed text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/20 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        className,
      )}
      {...props}
    />
  );
});
Textarea.displayName = "Textarea";
