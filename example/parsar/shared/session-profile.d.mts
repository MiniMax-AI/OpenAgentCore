export function sessionRestriction(
  agent: { harness: string; skill_ids: string[]; mcp_ids: string[] },
  runtime?: { environment: string },
): string | undefined;
