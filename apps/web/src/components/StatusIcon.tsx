export type StatusKind = "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";

interface StatusIconProps {
  status: StatusKind;
  title?: string;
}

/** The console's 14px status signature. Status colour never leaves this glyph. */
export function StatusIcon({ status, title }: StatusIconProps) {
  const common = {
    className: `status-icon status-${status}`,
    viewBox: "0 0 14 14",
    "aria-hidden": title ? undefined : true,
    role: title ? "img" : undefined,
  } as const;
  const label = title ? <title>{title}</title> : null;

  switch (status) {
    case "queued":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2.2 1.9" strokeLinecap="round" />
        </svg>
      );
    case "running":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="var(--status-track)" strokeWidth="1.5" />
          <g className="status-running-arc">
            <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeDasharray="25.9 34.56" transform="rotate(-90 7 7)" />
          </g>
        </svg>
      );
    case "completed":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="6.25" fill="currentColor" />
          <path d="m4.4 7.2 1.8 1.8 3.5-3.7" fill="none" stroke="var(--surface)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      );
    case "failed":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="6.25" fill="currentColor" />
          <path d="m4.9 4.9 4.2 4.2M9.1 4.9 4.9 9.1" fill="none" stroke="var(--surface)" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      );
    case "cancelled":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M3.9 10.1 10.1 3.9" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      );
    case "interrupted":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M4.6 7h4.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      );
  }
}
