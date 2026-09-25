import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Modal } from "./Modal";

/** A failed action: what failed, the reason, and the next step. */
export function ErrorDialog({ open, title, children, action, onClose }: {
  open: boolean;
  title: string;
  children: ReactNode;
  /** The next step, such as reading the current state again. */
  action?: { label: string; onClick: () => void };
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      footer={(
        <>
          <button type="button" className={action ? "button outline" : "button primary"} onClick={onClose}>{t("actions.close")}</button>
          {action ? <button type="button" className="button primary" onClick={() => { onClose(); action.onClick(); }}>{action.label}</button> : null}
        </>
      )}
    >
      <div className="confirm-dialog-body" role="alert">{children}</div>
    </Modal>
  );
}
