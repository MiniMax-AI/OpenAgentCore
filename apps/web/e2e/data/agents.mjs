// Synthetic saved Agents for the browser acceptance fixture: realistic instructions, tools,
// metadata and generation settings so list and detail pages have content.
const fn = (name, description, properties, required = Object.keys(properties)) => ({
  type: "function", name, description,
  parameters: { type: "object", properties, required, additionalProperties: false },
  strict: true,
});
const mcp = (label, url, allowed = null, credential = null) => ({
  type: "mcp", server_label: label, server_url: url, allowed_tools: allowed, require_approval: "never",
  ...(credential ? { x_agents_core: { credential_id: credential } } : {}),
});
const str = (description) => ({ type: "string", description });

export const agentDefinitions = [
  {
    name: "Incident responder", model: "gpt-5.1-codex", harness: "codex", reasoning: { effort: "high", summary: "auto" },
    instructions: "You are the on-call assistant for the web platform.\n\nWhen an alert arrives:\n1. Read the alert and the linked dashboard.\n2. Check recent deploys and error logs for the affected service.\n3. Propose the smallest safe mitigation and wait for approval before acting.\n4. Write a short incident note: impact, timeline, cause, next steps.",
    tools: [
      fn("lookup_deploys", "List deploys of a service in a time window.", { service: str("Service name"), since: str("ISO-8601 start") }),
      fn("page_owner", "Page the owning team with a summary.", { team: str("Team handle"), summary: str("One-paragraph summary") }),
      mcp("observability", "https://mcp.internal.example/observability", ["query_logs", "query_metrics"]),
    ],
    metadata: { team: "sre", tier: "production", runbook: "RB-0142" },
  },
  {
    name: "Support triage", model: "claude-sonnet-5", harness: "claude_sdk", reasoning: {},
    instructions: "Classify each incoming support ticket by product area and severity.\nAsk one clarifying question when the report lacks steps to reproduce.\nNever promise dates. Link the matching help-center article when one exists.",
    tools: [
      fn("search_help_center", "Search published help-center articles.", { query: str("Search text") }),
      mcp("crm", "https://mcp.example.com/crm-read", ["get_customer", "list_tickets"]),
    ],
    metadata: { team: "support", queue: "tier-1" },
  },
  {
    name: "Code reviewer", model: "gpt-5.1-codex", harness: "codex", reasoning: { effort: "medium" },
    instructions: "Review pull requests for correctness first, then clarity.\nComment only on issues that matter; group nits into one comment.\nFlag missing tests for changed behaviour.",
    tools: [mcp("github", "https://mcp.example.com/github", ["get_pull_request", "list_files", "create_review"])],
    metadata: { team: "platform", repo_scope: "monorepo" },
  },
  {
    name: "Data analyst", model: "MiniMax-M2", harness: "mcode", reasoning: {},
    instructions: "Answer questions about product metrics using the warehouse.\nShow the SQL you ran and state the time range and filters explicitly.\nIf data is missing, say so instead of estimating.",
    tools: [
      fn("run_sql", "Run a read-only SQL query against the analytics warehouse.", { sql: str("SELECT statement") }),
      fn("plot", "Render a chart from query results.", { kind: str("line | bar"), title: str("Chart title") }),
    ],
    metadata: { team: "data", warehouse: "analytics" },
  },
  {
    name: "Contract checker", model: "gpt-5.1-mini", harness: "codex", reasoning: {},
    instructions: "Compare a supplier contract against the standard template.\nList every deviation with the clause number, the template wording and the contract wording.\nMark liability, termination and data-protection deviations as high priority.",
    tools: [fn("fetch_template", "Fetch the current standard contract template.", { kind: str("Contract type") })],
    metadata: { team: "legal" },
  },
  {
    name: "Release notes writer", model: "claude-sonnet-5", harness: "claude_sdk", reasoning: {},
    instructions: "Draft release notes from merged pull requests since the last tag.\nGroup by user-visible change; leave out refactors and CI changes.",
    tools: [mcp("github", "https://mcp.example.com/github", ["list_merged_pull_requests", "get_release"])],
    metadata: { team: "platform", channel: "stable" },
  },
  {
    name: "Onboarding guide", model: "gpt-5.1-mini", harness: "codex", reasoning: {},
    instructions: "Help new employees find internal documentation and set up their accounts.\nKeep answers short and link the source page.",
    tools: [mcp("docs", "https://mcp.internal.example/docs", null)],
    metadata: { team: "people-ops" },
  },
  {
    name: "Churn forecaster", model: "MiniMax-M2", harness: "mcode", reasoning: { effort: "high" },
    instructions: "Estimate 90-day churn risk per account from usage and support signals.\nExplain the top three drivers for each high-risk account.",
    tools: [fn("run_sql", "Run a read-only SQL query against the analytics warehouse.", { sql: str("SELECT statement") })],
    metadata: { team: "data", model_version: "2026-09" },
  },
  {
    name: "Log summarizer", model: "gpt-5.1-codex", harness: "codex", reasoning: {},
    instructions: "Summarise error logs for the last hour: top error signatures, first seen, count, affected hosts.",
    tools: [mcp("observability", "https://mcp.internal.example/observability", ["query_logs"])],
    metadata: { team: "sre" },
  },
  {
    name: "Staging smoke tester", model: "gpt-5.1-mini", harness: "codex", reasoning: {},
    instructions: "Run the staging smoke checklist after each deploy and report failures with request IDs.",
    tools: [fn("http_check", "Call an endpoint and return status and latency.", { url: str("Absolute URL") })],
    metadata: { team: "qa", environment: "staging" },
  },
];

/** Which project owns each definition (index into the demo projects). */
export const agentProject = [0, 1, 2, 0, 1, 0, 1, 1, 2, 3];

/** Prompt and reply pairs per Agent so Session transcripts read like real work. */
export const agentConversations = {
  "Incident responder": [
    ["Alert: p95 latency on checkout-api above 2 s for 10 minutes. What changed?", "checkout-api was deployed at 09:42 (build 7f3c2a9). Errors started 3 minutes later and are limited to the new pods.\n\n**Proposed mitigation:** roll back to build 51d418a. Waiting for approval before acting."],
    ["Approved. Roll back and write the incident note.", "Rollback finished at 10:05; p95 is back to 380 ms.\n\n- **Impact:** 18 minutes of slow checkouts, no failed orders\n- **Cause:** N+1 query in the new discount lookup\n- **Next:** add a query-count test to the discount service"],
  ],
  "Support triage": [
    ["Customer says exports to CSV are empty since yesterday. Ticket #48213.", "Classified as **Reports / high**. The report omits the date range, so I asked which range they exported. Linked article: \"Exporting reports to CSV\"."],
    ["They used 'Last 7 days' on the EU workspace.", "Reproduced on an EU test workspace: the export job times out and returns an empty file. Escalated to the reports team with the request ID."],
  ],
  "Code reviewer": [
    ["Review PR #1287: add retry to the payment webhook handler.", "Two issues matter:\n\n1. Retries are not idempotent: a retried webhook can charge twice. Key the handler on the event ID.\n2. No test covers the retry path.\n\nThe remaining comments are nits, grouped in one comment."],
  ],
  "Data analyst": [
    ["How many workspaces were active last week compared to the week before?", "Active workspaces (at least one Session):\n\n| Week | Active |\n|---|---|\n| Sep 8–14 | 1,284 |\n| Sep 15–21 | 1,367 |\n\nThat is **+6.5%**. Range: Monday to Sunday UTC, excluding internal workspaces."],
    ["Break it down by plan.", "Team +9.1%, Business +4.2%, Enterprise flat. The Team growth comes from 61 new workspaces created during the onboarding campaign."],
  ],
  "Contract checker": [
    ["Check the Acme Logistics supplier contract against our template.", "Found 4 deviations. High priority:\n\n- **§9.2 Liability:** cap lowered from 12 months of fees to 3 months\n- **§14 Data protection:** sub-processors can be added without notice\n\nThe other two concern payment terms (§5.1) and notice periods (§17.3)."],
  ],
  "Release notes writer": [
    ["Draft release notes since v2.14.0.", "## v2.15.0\n\n- **Sessions:** failed Turns now show the error next to the conversation\n- **Files:** uploads up to 512 MiB\n- **Fixes:** exports no longer time out on large workspaces\n\nLeft out 23 refactor and CI changes."],
  ],
  "Onboarding guide": [
    ["Where do I request access to the staging cluster?", "Request it in the access portal under **Infrastructure → Staging cluster**; your manager approves it. Source: \"Getting access to environments\" in the engineering handbook."],
  ],
  "Churn forecaster": [
    ["Which enterprise accounts are at risk this quarter?", "Three accounts score above 0.7:\n\n1. **Northwind** (0.82): seats used fell 40%, two P1 tickets open\n2. **Globex** (0.76): champion left, no logins from admins in 30 days\n3. **Initech** (0.71): renewal in 45 days, usage flat"],
  ],
  "Log summarizer": [
    ["Summarize errors from the last hour.", "Top signatures:\n\n- `TimeoutError: upstream inventory` — 412 hits, first 09:12, web-01…web-04\n- `ConnectionReset: redis-cache` — 57 hits, first 09:40, worker-02\n\nNo new signatures compared to yesterday."],
  ],
  "Staging smoke tester": [
    ["Run the smoke checklist on the 10:30 staging deploy.", "18 of 19 checks passed. **Failed:** `POST /v1/exports` returned 500 (request req_8d2f1c). Everything else is within latency budgets."],
  ],
};
