export type PatchChangeKind = "add" | "modify" | "delete";

export interface ParsedPatchChange {
  path: string;
  kind: PatchChangeKind;
  diff: string;
  additions: number;
  deletions: number;
  lines: Array<{ text: string; kind: "add" | "delete" | "hunk" | "header" | "context" }>;
}

export interface ParsedApplyPatch {
  changes: ParsedPatchChange[];
  additions: number;
  deletions: number;
}

function exactKeys(value: Record<string, unknown>, allowed: string[]): boolean {
  return Object.keys(value).every((key) => allowed.includes(key));
}

function parseKind(value: unknown): PatchChangeKind | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const kind = value as Record<string, unknown>;
  if (!exactKeys(kind, ["type", "move_path"])) return null;
  if (kind.move_path !== undefined && kind.move_path !== null && typeof kind.move_path !== "string") return null;
  if (kind.type === "add") return "add";
  if (kind.type === "delete") return "delete";
  if (kind.type === "update") return "modify";
  return null;
}

function parseDiff(diff: string): Pick<ParsedPatchChange, "lines" | "additions" | "deletions"> {
  let additions = 0;
  let deletions = 0;
  const lines = diff.split("\n").map((text) => {
    let kind: ParsedPatchChange["lines"][number]["kind"] = "context";
    if (text.startsWith("@@")) kind = "hunk";
    else if (text.startsWith("+++ ") || text.startsWith("--- ") || text.startsWith("diff ")) kind = "header";
    else if (text.startsWith("+")) {
      kind = "add";
      additions += 1;
    } else if (text.startsWith("-")) {
      kind = "delete";
      deletions += 1;
    }
    return { text, kind };
  });
  return { lines, additions, deletions };
}

export function parseApplyPatch(argumentsValue: unknown): ParsedApplyPatch | null {
  if (!argumentsValue || typeof argumentsValue !== "object" || Array.isArray(argumentsValue)) return null;
  const root = argumentsValue as Record<string, unknown>;
  if (!exactKeys(root, ["changes"]) || !Array.isArray(root.changes) || root.changes.length === 0) return null;

  const changes: ParsedPatchChange[] = [];
  for (const value of root.changes) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    const change = value as Record<string, unknown>;
    if (!exactKeys(change, ["path", "kind", "diff"])) return null;
    if (typeof change.path !== "string" || !change.path.trim() || typeof change.diff !== "string") return null;
    const kind = parseKind(change.kind);
    if (!kind) return null;
    changes.push({ path: change.path, kind, diff: change.diff, ...parseDiff(change.diff) });
  }

  return {
    changes,
    additions: changes.reduce((sum, change) => sum + change.additions, 0),
    deletions: changes.reduce((sum, change) => sum + change.deletions, 0),
  };
}
