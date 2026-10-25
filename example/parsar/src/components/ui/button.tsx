import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

/**
 * The one button. 36px tall, 10px radius, 14px/500 label, spring press.
 * `default` is the ink primary, reserved for the one primary action of a
 * screen; `outline` is paper with a strong hairline.
 */
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:ring-offset-1 focus-visible:ring-offset-surface disabled:pointer-events-none disabled:opacity-50 active:scale-[0.97] motion-reduce:active:scale-100 [&>svg]:h-4 [&>svg]:w-4 [&>svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          "app-shadow-control bg-surface-emphasis text-fg-on-emphasis hover:bg-fg-emphasis",
        destructive:
          "app-shadow-control bg-danger text-fg-on-emphasis hover:bg-danger-emphasis",
        outline:
          "app-shadow-control border border-line-strong bg-surface text-fg hover:app-hover disabled:border-line disabled:bg-transparent disabled:shadow-none",
        secondary: "bg-surface-muted text-fg hover:app-pressed",
        ghost: "text-fg-muted hover:app-hover hover:text-fg",
        link: "text-fg underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4 text-sm",
        sm: "h-8 px-3 text-sm",
        lg: "h-10 px-5 text-sm",
        // Entry surfaces use a 44px control.
        xl: "h-11 px-5 text-base",
        icon: "h-9 w-9",
      },
      /* Shape follows function: rounded (default) for controls, pill for
         chips-as-buttons, circle for icon buttons, square for accents. */
      shape: {
        rounded: "rounded-[10px]",
        pill: "rounded-full px-4",
        circle: "rounded-full",
        square: "rounded-none",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
      shape: "rounded",
    },
  },
);

interface ButtonProps
  extends
    React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, shape, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, shape }), className)}
        ref={ref}
        {...props}
      />
    );
  },
);
Button.displayName = "Button";
