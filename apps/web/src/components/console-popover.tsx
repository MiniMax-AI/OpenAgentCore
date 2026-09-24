import type { ReactElement, ReactNode } from "react";

import { Popover, PopoverPopup, PopoverTitle, PopoverTrigger } from "@/components/ui/popover";

/**
 * The console's anchored popover: coss ui's Base UI popover as a small overlay
 * card for a glance at one object and the ways onward. It opens on click,
 * closes on Escape or an outside click, and returns focus to its trigger. The
 * body mounts only while open, so queries inside it run on demand.
 */
export function ConsolePopover({ trigger, title, meta, children, actions, side = "bottom", align = "center" }: {
  /** The element that opens the popover; it keeps its own classes and label. */
  trigger: ReactElement;
  title: ReactNode;
  /** A quiet line under the title, such as an ID. */
  meta?: ReactNode;
  children: ReactNode;
  /** Links onward, set apart at the foot. */
  actions?: ReactNode;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
}) {
  return (
    <Popover>
      <PopoverTrigger render={trigger} />
      <PopoverPopup
        side={side}
        align={align}
        sideOffset={8}
        className="w-72 rounded-[14px] border-0 shadow-(--shadow-overlay) before:hidden"
      >
        <div className="console-popover">
          <header className="console-popover-head">
            <PopoverTitle className="text-[14px] leading-5 font-semibold tracking-[-0.01em]">{title}</PopoverTitle>
            {meta ? <span className="console-popover-meta">{meta}</span> : null}
          </header>
          {children}
          {actions ? <footer className="console-popover-actions">{actions}</footer> : null}
        </div>
      </PopoverPopup>
    </Popover>
  );
}
