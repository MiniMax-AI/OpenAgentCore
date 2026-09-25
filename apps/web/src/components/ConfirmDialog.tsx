import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Modal } from "./Modal";

/** One confirmation grammar for destructive actions across the console. */
export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  busyLabel,
  busy,
  error,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel: string;
  busyLabel: string;
  busy: boolean;
  error?: string | null;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      title={title}
      onClose={() => { if (!busy) onClose(); }}
      footer={(
        <>
          <button type="button" className="button outline" disabled={busy} onClick={onClose}>{t("actions.cancel")}</button>
          <button type="button" className="button danger" disabled={busy} onClick={onConfirm}>{busy ? busyLabel : confirmLabel}</button>
        </>
      )}
    >
      <div className="confirm-dialog-body">
        {children}
        {error ? <p className="confirm-dialog-error" role="alert">{error}</p> : null}
      </div>
    </Modal>
  );
}
