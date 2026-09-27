import { AlertTriangle, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

interface ErrorStateProps {
  title: string;
  description?: string;
  detail?: string;
  hint?: string;
  onRetry?: () => void;
  action?: ReactNode;
  className?: string;
}

/** A flat, durable error state for failures that must not fade away. */
export function ErrorState({
  title,
  description,
  detail,
  hint,
  onRetry,
  action,
  className,
}: ErrorStateProps) {
  const { t } = useTranslation();
  return (
    <div className={["error-state", className].filter(Boolean).join(" ")}>
      <div className="error-state-heading">
        <AlertTriangle size={14} strokeWidth={1.5} aria-hidden="true" />
        <div className="error-state-copy">
          <p className="error-state-title">{title}</p>
          {description ? <p className="error-state-description">{description}</p> : null}
          {detail ? <pre className="error-state-detail">{detail}</pre> : null}
          {hint ? <p className="error-state-hint">{hint}</p> : null}
        </div>
      </div>
      {onRetry || action ? (
        <div className="error-state-actions">
          {onRetry ? (
            <button className="button outline" type="button" onClick={onRetry}>
              <RefreshCw size={14} strokeWidth={1.5} aria-hidden="true" />
              {t("actions.retry")}
            </button>
          ) : null}
          {action}
        </div>
      ) : null}
    </div>
  );
}
