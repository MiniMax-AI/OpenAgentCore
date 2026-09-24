import NumberFlow, { type Format } from "@number-flow/react";
import { useTranslation } from "react-i18next";

import { MISSING } from "../lib/format";

export type LiveNumberFormat = "integer" | "compact" | "percent";

/** Intl options matching formatInteger, formatCompact and formatPercent in lib/format. */
function formatOptions(value: number, format: LiveNumberFormat, locale: string | undefined): Format {
  if (format === "percent") return { style: "percent", maximumFractionDigits: value > 0 && value < 0.1 ? 1 : 0 };
  if (format === "integer" || Math.abs(value) < 10_000) return { maximumFractionDigits: 0 };
  const integerDigits = new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 })
    .formatToParts(value).filter((part) => part.type === "integer").map((part) => part.value).join("").length;
  return { notation: "compact", maximumFractionDigits: integerDigits === 1 ? 2 : 1 };
}

/**
 * A live figure. When a refresh changes it, its digits roll to the new value
 * (NumberFlow, https://number-flow.barvian.me) instead of swapping, so a
 * change is noticed without a highlight; the first render does not animate.
 * Missing figures stay an em dash.
 */
export function LiveNumber({ value, format = "integer" }: { value: number | null | undefined; format?: LiveNumberFormat }) {
  const { i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  if (value === null || value === undefined || !Number.isFinite(value)) return <>{MISSING}</>;
  return <NumberFlow className="live-number" value={value} locales={locale} format={formatOptions(value, format, locale)} />;
}
