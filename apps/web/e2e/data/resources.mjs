// Synthetic Resources for the browser acceptance fixture (Skills, Files, Templates, Vaults).
let seed = 7;
const rand = () => ((seed = (seed * 1664525 + 1013904223) % 4294967296) / 4294967296);
const hex = (n) => Array.from({ length: n }, () => Math.floor(rand() * 16).toString(16)).join("");
const uuid = () => `${hex(8)}-${hex(4)}-4${hex(3)}-8${hex(3)}-${hex(12)}`;

export function buildResources(now) {
  seed = 7;
  const skills = [
    ["report", "Create quarterly and incident reports from structured notes.", 3, 2],
    ["triage", "Sort incoming issues by severity and owner.", 2, 2],
    ["sql-review", "Review SQL migrations for locking and data loss.", 1, 1],
    ["release-notes", "Draft release notes from merged changes.", 4, 4],
    ["pdf-extract", "Extract tables and text from PDF documents.", 2, 1],
  ].map(([name, description, latest, defaultVersion], index) => ({
    id: `skill_${uuid()}`, object: "skill", created_at: now - 86400 * (2 + index * 3) - 3600 * index,
    name, description, default_version: String(defaultVersion), latest_version: String(latest),
  }));
  const skillVersions = new Map(skills.map((skill) => [skill.id, Array.from({ length: Number(skill.latest_version) }, (_, index) => ({
    id: `skillver_${uuid()}`, object: "skill.version", created_at: skill.created_at + index * 86400 * 0.7,
    skill_id: skill.id, version: String(index + 1), name: skill.name, description: skill.description,
  })).reverse()]));

  const files = [
    ["incident-2026-09-21.md", 18_432], ["customers.csv", 2_418_112], ["contract-template.docx", 96_512], ["schema.sql", 7_340],
    ["logs-web-01.tar.gz", 48_211_968], ["notes.txt", 312], ["q3-metrics.xlsx", 391_224], ["input.csv", 42], ["empty.bin", 0],
  ].map(([filename, bytes], index) => ({
    id: `file-${uuid()}`, object: "file", bytes, created_at: now - 3600 * (3 + index * 17), filename, purpose: "user_data",
    status: "processed", expires_at: null, status_details: null,
  }));

  const templates = [
    { name: "Report builder", network: { access: "restricted", allowed_domains: ["pypi.org", "files.pythonhosted.org"] },
      packages: { npm: ["typescript@5.8.3"], python: ["packaging==26.0", "pandas==2.3.1"], system: ["jq"] },
      files: [{ type: "inline", path: "/workspace/config/settings.json", size_bytes: 128 }, { type: "file_id", path: "/workspace/data/input.csv", file_id: files[7].id }],
      skills: [{ type: "skill_reference", skill_id: skills[0].id, version: null }, { type: "skill_reference", skill_id: skills[3].id, version: "latest" }] },
    { name: "Python analysis", network: { access: "enabled", allowed_domains: [] },
      packages: { npm: [], python: ["numpy==2.3.0", "polars==1.31.0"], system: [] }, files: [], skills: [{ type: "skill_reference", skill_id: skills[4].id, version: "2" }] },
    { name: "Offline sandbox", network: { access: "disabled", allowed_domains: [] }, packages: { npm: [], python: [], system: [] }, files: [], skills: [] },
    { name: "Web research", network: { access: "restricted", allowed_domains: ["en.wikipedia.org", "arxiv.org", "github.com"] },
      packages: { npm: ["playwright@1.55.0"], python: [], system: ["chromium"] }, files: [], skills: [] },
  ].map((template, index) => ({
    id: uuid(), object: "agent.environment.template", name: template.name,
    created_at: now - 86400 * (4 + index * 5), updated_at: now - 86400 * (1 + index * 2),
    capability_directories: index === 0 ? ["/workspace/capabilities"] : [], network: template.network, packages: template.packages,
    files: template.files, plugins: [], skills: template.skills,
  }));

  const vaults = [["Internal services", 2], ["GitHub MCP", 1], ["Customer CRM", 3], ["Sandbox testing", 0]].map(([name, count], index) => ({
    vault: { id: uuid(), object: "vault", created_at: now - 86400 * (6 + index * 4), name, metadata: {} }, count,
  }));
  const credentialNames = ["Internal MCP", "Search MCP", "GitHub", "CRM read", "CRM write", "CRM export"];
  let credentialIndex = 0;
  const credentials = new Map(vaults.map(({ vault, count }) => [vault.id, Array.from({ length: count }, (_, index) => {
    const name = credentialNames[credentialIndex++ % credentialNames.length];
    const created = vault.created_at + 3600 * (index + 1);
    return { id: uuid(), vault_id: vault.id, name, object: "vault.credential",
      auth: { type: "static_bearer", mcp_server_url: `https://mcp.example.com/${name.toLowerCase().replace(/\s+/g, "-")}` },
      created_at: created, updated_at: created + (index % 2 ? 86400 : 0) };
  }).reverse()]));

  return {
    skills, skillVersions, files, templates,
    vaults: vaults.map(({ vault }) => vault), credentials,
  };
}
