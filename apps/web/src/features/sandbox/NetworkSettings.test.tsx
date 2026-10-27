import type { SandboxDeployment } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { NetworkForm } from "./NetworkSettings";
import { domainIssues, parseDomains } from "./network-policy";

const deployment = (provider: "microsandbox" | "e2b") => ({ provider, specification: { resources: { cpus: 2, memory_mib: 4096 }, network: { public: true, domains: ["gitlab.example.com"] } } }) as SandboxDeployment;

describe("network policy", () => {
  it("validates domains with the generated rule", () => {
    expect(domainIssues(parseDomains("gitlab.example.com\n\n a.b-c.example \nGit.Example.com"))).toEqual([]);
    expect(domainIssues(["*.example.com", "-x.com", "a.com", "a.com", "a".repeat(254)]).map((issue) => issue.problem)).toEqual(["invalid", "invalid", "duplicate", "too_long"]);
    expect(domainIssues(Array.from({ length: 101 }, (_, index) => `h${index}.example.com`))).toEqual([{ problem: "too_many" }]);
  });

  it("enables both controls where the provider supports them", () => {
    const html = renderToStaticMarkup(<NetworkForm deployment={deployment("microsandbox")} disabled={false} onSave={async () => true} />);
    expect(html).toContain("Allow public internet");
    expect(html).toContain("gitlab.example.com");
    expect(html).not.toMatch(/<textarea[^>]*disabled/);
    expect(html).not.toMatch(/<input[^>]*disabled/);
  });

  it("disables the domain list where the provider cannot reach a private network", () => {
    const html = renderToStaticMarkup(<NetworkForm deployment={deployment("e2b")} disabled={false} onSave={async () => true} />);
    expect(html).toMatch(/<textarea[^>]*disabled/);
    expect(html).not.toMatch(/<input[^>]*disabled/);
    expect(html).toContain("cannot reach your private network");
  });
});
