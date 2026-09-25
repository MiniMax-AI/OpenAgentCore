import { X } from "lucide-react";
import { useEffect, useId, useRef, useState, type PropsWithChildren, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

interface ModalProps extends PropsWithChildren {
  open: boolean;
  title: string;
  footer?: ReactNode;
  onClose: () => void;
  /** A wider dialog for charts and facts; the default suits a form. */
  wide?: boolean;
}

export function Modal({ open, title, footer, onClose, wide = false, children }: ModalProps) {
  const { t } = useTranslation();
  const dialogRef = useRef<HTMLElement>(null);
  const onCloseRef = useRef(onClose);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const wasOpenRef = useRef(false);
  const titleId = useId();
  const [mounted, setMounted] = useState(open);
  const [closing, setClosing] = useState(false);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (open) {
      if (!wasOpenRef.current) {
        previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      }
      wasOpenRef.current = true;
      setMounted(true);
      setClosing(false);
      return;
    }
    wasOpenRef.current = false;
    if (!mounted) return;

    setClosing(true);
    const timer = window.setTimeout(() => {
      setMounted(false);
      setClosing(false);
    }, 200);
    return () => window.clearTimeout(timer);
  }, [mounted, open]);

  useEffect(() => {
    if (!open || !mounted) return;

    const dialog = dialogRef.current;
    const focusableSelector = [
      "button:not([disabled])",
      "input:not([disabled])",
      "textarea:not([disabled])",
      "select:not([disabled])",
      "a[href]",
      "[tabindex]:not([tabindex='-1'])",
    ].join(",");
    const frame = window.requestAnimationFrame(() => {
      const preferred = dialog?.querySelector<HTMLElement>("input:not([disabled]), textarea:not([disabled]), select:not([disabled])");
      const first = preferred ?? dialog?.querySelector<HTMLElement>(focusableSelector) ?? dialog;
      first?.focus();
    });

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialog) return;

      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector));
      if (!focusable.length) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!dialog.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first)?.focus();
      } else if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      window.cancelAnimationFrame(frame);
      document.removeEventListener("keydown", handleKeyDown);
      previousFocusRef.current?.focus();
    };
  }, [mounted, open]);

  if (!mounted) return null;

  return (
    <div
      className={`modal-backdrop ${closing ? "closing" : ""}`}
      role="presentation"
      aria-hidden={closing || undefined}
      onMouseDown={() => {
        if (!closing) onClose();
      }}
    >
      <section
        ref={dialogRef}
        className={wide ? "modal-card modal-card-wide" : "modal-card"}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="modal-header">
          <h2 id={titleId}>{title}</h2>
          <button className="icon-button" type="button" onClick={onClose} aria-label={t("actions.closeDialog")}>
            <X size={14} strokeWidth={1.5} />
          </button>
        </header>
        <div className="modal-body">{children}</div>
        {footer ? <footer className="modal-footer">{footer}</footer> : null}
      </section>
    </div>
  );
}
