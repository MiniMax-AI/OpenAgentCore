/** Locale-aware formatting for console figures. Missing values render as an em dash. */

export const MISSING = "—";

export function formatInteger(value: number | null | undefined, locale?: string): string {
  return value === null || value === undefined || !Number.isFinite(value) ? MISSING : Math.round(value).toLocaleString(locale);
}

export function formatCompact(value: number | null | undefined, locale?: string): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return MISSING;
  if (Math.abs(value) < 10_000) return Math.round(value).toLocaleString(locale);
  const oneDecimal = new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 });
  // A single integer digit ("1.1M") hides real differences; keep two decimals there ("1.04M"), but not for "415.7万".
  const integerDigits = oneDecimal.formatToParts(value).filter((part) => part.type === "integer").map((part) => part.value).join("").length;
  return integerDigits === 1
    ? new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 2 }).format(value)
    : oneDecimal.format(value);
}

export function formatPercent(ratio: number | null | undefined, locale?: string): string {
  if (ratio === null || ratio === undefined || !Number.isFinite(ratio)) return MISSING;
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: ratio > 0 && ratio < 0.1 ? 1 : 0 }).format(ratio);
}

export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return MISSING;
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  // One decimal below 100, without a trailing ".0": "1 GiB", "1.5 GiB", "318 MiB".
  return `${amount >= 100 || unit === 0 ? Math.round(amount) : amount.toFixed(1).replace(/\.0$/, "")} ${units[unit]}`;
}

/** Durations in seconds, as a compact unit string (850 ms, 12.4 s, 4m 12s, 3h 5m). */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return MISSING;
  // Sub-10 ms values keep decimals, so a fast database never reads "1 ms" on every tick.
  if (seconds < 0.00995) return `${new Intl.NumberFormat("en", { maximumFractionDigits: seconds < 0.001 ? 2 : 1 }).format(seconds * 1000)} ms`;
  if (seconds < 0.9995) return `${Math.round(seconds * 1000)} ms`;
  if (seconds < 9.95) return `${seconds.toFixed(1)} s`;
  const whole = Math.round(seconds);
  if (whole < 60) return `${whole} s`;
  const minutes = Math.floor(whole / 60);
  if (minutes < 60) return `${minutes}m ${whole % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function formatCores(value: number | null | undefined, locale?: string): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return MISSING;
  return value.toLocaleString(locale, { maximumFractionDigits: value < 10 ? 2 : 1 });
}

/** Date and time; the year is left out for dates in the current year. */
export function formatDateTime(epochSeconds: number | null | undefined, locale?: string, now: Date = new Date()): string {
  if (epochSeconds === null || epochSeconds === undefined || !Number.isFinite(epochSeconds)) return MISSING;
  const date = new Date(epochSeconds * 1000);
  const sameYear = date.getFullYear() === now.getFullYear();
  return new Intl.DateTimeFormat(locale, {
    ...(sameYear ? {} : { year: "numeric" }),
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date);
}

export function formatClock(epochMilliseconds: number | null | undefined, locale?: string): string {
  if (epochMilliseconds === null || epochMilliseconds === undefined || !Number.isFinite(epochMilliseconds)) return MISSING;
  return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).format(new Date(epochMilliseconds));
}

export function formatRelative(epochSeconds: number | null | undefined, nowSeconds: number, locale?: string): string {
  if (epochSeconds === null || epochSeconds === undefined || !Number.isFinite(epochSeconds)) return MISSING;
  // These are past events; a future value is clock skew between Core and the browser.
  const delta = Math.min(0, epochSeconds - nowSeconds);
  const magnitude = Math.abs(delta);
  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (magnitude < 45) return format.format(0, "second");
  if (magnitude < 3_600) return format.format(Math.round(delta / 60), "minute");
  if (magnitude < 86_400) return format.format(Math.round(delta / 3_600), "hour");
  return format.format(Math.round(delta / 86_400), "day");
}

export function shortId(id: string): string {
  return id.length > 14 ? `${id.slice(0, 6)}…${id.slice(-4)}` : id;
}

/** A policy period in its largest whole unit, as words: "5 minutes / 5 分钟", "24 hours / 24 小时", "7 days / 7 天". */
export function formatPeriod(seconds: number | null | undefined, locale?: string): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return MISSING;
  const [amount, unit] = seconds >= 172_800 && seconds % 86_400 === 0 ? [seconds / 86_400, "day"]
    : seconds >= 3_600 && seconds % 3_600 === 0 ? [seconds / 3_600, "hour"]
      : seconds >= 60 ? [seconds / 60, "minute"] : [seconds, "second"];
  const text = new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "long", maximumFractionDigits: 1 }).format(amount);
  // Chinese copy spaces a figure from its unit, as the console's range labels do ("24 小时").
  return locale?.startsWith("zh") ? text.replace(/^([\d.,]+)(?=[^\d\s.,])/, "$1 ") : text;
}

/** A measured span, rounded to its largest unit, as words: 7,300 s reads "2 hours / 2 小时", 90 s "2 minutes". */
export function formatSpan(seconds: number, locale?: string): string {
  const unit = seconds >= 172_800 ? 86_400 : seconds >= 3_600 ? 3_600 : seconds >= 60 ? 60 : 1;
  return formatPeriod(Math.round(seconds / unit) * unit, locale);
}

/** A chart bucket as words: "1 分钟", "15 minutes", "2 hours". */
export function formatBucket(seconds: number, locale?: string): string {
  const [amount, unit] = seconds >= 3600 ? [seconds / 3600, "hour"] : [Math.max(1, seconds / 60), "minute"];
  return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "long", maximumFractionDigits: 0 }).format(amount);
}
