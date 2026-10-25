import * as React from "react";
import { cn } from "../../lib/utils";

/**
 * The one text field. 36px, paper, strong hairline, control shadow;
 * indigo border + ring on focus.
 */
export const Input = React.forwardRef<
  HTMLInputElement,
  Omit<React.InputHTMLAttributes<HTMLInputElement>, "size"> & {
    /** `lg` (44px) is for entry surfaces; forms use 36px. */
    size?: "default" | "lg";
  }
>(({ className, type, size = "default", ...props }, ref) => {
  return (
    <input
      type={type}
      className={cn(
        "app-shadow-control flex w-full rounded-[10px] border border-line-strong bg-surface text-sm",
        "text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/20 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        size === "lg" ? "h-11 px-4 text-base" : "h-9 px-3.5",
        className,
      )}
      ref={ref}
      {...props}
    />
  );
});
Input.displayName = "Input";
