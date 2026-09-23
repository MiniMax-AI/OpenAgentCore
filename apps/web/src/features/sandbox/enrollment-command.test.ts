import { describe, expect, it } from "vitest";
import { enrollmentCommand } from "./enrollment-command";
import { sandboxAdminBaseUrl } from "./SandboxContext";

describe("sandbox connection and enrollment", () => {
  it("derives a separate admin prefix for local and prefixed remote Core deployments", () => {
    expect(sandboxAdminBaseUrl("/v1")).toBe("/core/v1/sandbox");
    expect(sandboxAdminBaseUrl("https://core.example/prefix/v1/")).toBe("https://core.example/prefix/core/v1/sandbox");
  });
  it("writes the one-time credential as a private file rather than a process argument", () => {
    const command = enrollmentCommand("fixture-token", "https://core.example");
    expect(command).toContain("umask 077");
    expect(command).toContain("chmod 600");
    expect(command).toContain("--enrollment-token-file");
    expect(command.split("\n").find((line) => line.startsWith("parsar-sandbox-node register"))).not.toContain("fixture-token");
    expect(command).toContain("rm /var/lib/parsar/sandbox-node/enrollment-token &&");
  });
  it("quotes command values and avoids a token colliding with the heredoc terminator", () => {
    const command = enrollmentCommand("PARSAR_ENROLLMENT_TOKEN", "https://core.example/o'neil");
    expect(command).toContain("<<'PARSAR_ENROLLMENT_TOKEN_END'");
    expect(command).toContain("'https://core.example/o'\\''neil'");
  });
});
