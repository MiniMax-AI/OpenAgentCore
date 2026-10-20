/* eslint-disable react-refresh/only-export-components */
import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "../../lib/utils";

/**
 * The one dialog, built like the detail rail: a floating paper panel (8px
 * radius, hairline, floating shadow, pop-in / pop-out) whose header and
 * footer are hairline-bounded bars (48px header with the 16px/500 title
 * and the ghost close button; footer with the actions right-aligned), and a
 * 16px-padded body between them.
 */
export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogPortal = DialogPrimitive.Portal;
export const DialogClose = DialogPrimitive.Close;

export const DialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      "fixed inset-0 z-50 bg-surface-inverse/30 data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out",
      className,
    )}
    {...props}
  />
));
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName;

export interface DialogContentProps extends React.ComponentPropsWithoutRef<
  typeof DialogPrimitive.Content
> {
  /** Default true; set false for confirm dialogs where action buttons are the only exits. */
  showCloseButton?: boolean;
}

export const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>(({ className, children, showCloseButton = true, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "app-shadow-floating fixed left-1/2 top-1/2 z-50 grid max-h-[88dvh] w-[calc(100vw-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 gap-6 overflow-y-auto rounded-2xl border border-line bg-surface p-7 text-sm text-fg animate-pop-in data-[state=closed]:animate-pop-out focus:outline-none",
        className,
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close
          className="absolute right-4 top-4 inline-flex h-9 w-9 items-center justify-center rounded-lg text-fg-muted hover:app-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          aria-label="Close"
        >
          <X className="h-4 w-4" strokeWidth={1.5} />
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </DialogPortal>
));
DialogContent.displayName = DialogPrimitive.Content.displayName;

export function DialogHeader({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "-mx-7 -mt-7 flex min-h-16 flex-col justify-center gap-1 px-7 pt-6 pb-1 pr-14",
        className,
      )}
      {...props}
    />
  );
}
DialogHeader.displayName = "DialogHeader";

export function DialogFooter({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "-mx-7 -mb-7 flex items-center justify-end gap-2.5 border-t border-line-muted px-7 py-4",
        className,
      )}
      {...props}
    />
  );
}
DialogFooter.displayName = "DialogFooter";

export const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn("text-lg font-semibold leading-snug tracking-tight text-fg", className)}
    {...props}
  />
));
DialogTitle.displayName = DialogPrimitive.Title.displayName;

export const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn("text-sm text-fg-muted", className)}
    {...props}
  />
));
DialogDescription.displayName = DialogPrimitive.Description.displayName;
