export type HostShell = "posix" | "powershell";
export type ExecutorInstall = { kind: "unavailable" } | { kind: "ready"; commands: Record<HostShell, string> };
