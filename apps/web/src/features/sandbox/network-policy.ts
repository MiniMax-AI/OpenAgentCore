import { deploymentContract, type SandboxProvider } from "@oac/agents-client";

const rule = deploymentContract.network;
const pattern = new RegExp(rule.domain_pattern);

export type DomainProblem = "invalid" | "too_long" | "duplicate";
export type DomainIssue = { domain: string; problem: DomainProblem } | { problem: "too_many" };

/** One host name per line, lowercased like the API does; blank lines are ignored. */
export function parseDomains(text: string): string[] {
  return text.split("\n").map((line) => line.trim().toLowerCase()).filter(Boolean);
}

/** Checks a domain list against the generated rule while it is typed. */
export function domainIssues(domains: readonly string[]): DomainIssue[] {
  const issues: DomainIssue[] = [];
  if (domains.length > rule.max_domains) issues.push({ problem: "too_many" });
  const seen = new Set<string>();
  for (const domain of domains) {
    if (seen.has(domain)) issues.push({ domain, problem: "duplicate" });
    seen.add(domain);
    if (domain.length > rule.max_domain_length) issues.push({ domain, problem: "too_long" });
    else if (!pattern.test(domain)) issues.push({ domain, problem: "invalid" });
  }
  return issues;
}

/** What the current Provider declares it can enforce. */
export function networkSupport(provider: SandboxProvider): { restrict: boolean; private_domains: boolean } {
  return deploymentContract.providers[provider].network;
}
export const maxDomains = rule.max_domains;
