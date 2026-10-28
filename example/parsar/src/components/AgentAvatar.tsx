import { cn } from "../lib/utils";

export function AgentAvatar({
  name,
  size = "default",
}: {
  name: string;
  size?: "default" | "lg";
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-xl bg-surface-emphasis font-semibold text-fg-on-emphasis",
        size === "lg" ? "h-12 w-12 text-lg" : "h-9 w-9 text-sm",
      )}
    >
      {Array.from(name.trim())[0]?.toUpperCase() || "A"}
    </span>
  );
}
