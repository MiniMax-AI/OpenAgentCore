import { readFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";

// This case is a client of the ordinary Parsar resource API, not a server feature.
const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i],
    value = process.argv[i + 1];
  if (
    !["--url", "--model", "--app", "--skills", "--scopes"].includes(name) ||
    !value ||
    args.has(name)
  )
    throw new Error(
      "Usage: node configure.mjs --url URL --model MODEL_ID --app BOT_ID --skills SKILL_ID[,SKILL_ID] [--scopes SCOPES]",
    );
  args.set(name, value);
}
for (const name of ["--url", "--model", "--app", "--skills"])
  if (!args.has(name)) throw new Error(`Missing ${name}`);
const base = new URL(args.get("--url"));
if (
  !["localhost", "127.0.0.1", "[::1]"].includes(base.hostname) ||
  base.username ||
  base.password ||
  !["http:", "https:"].includes(base.protocol)
)
  throw new Error("Use the forwarded loopback Parsar URL.");
async function api(path, method, body) {
  const response = await fetch(new URL(path, base), {
    method,
    redirect: "error",
    signal: AbortSignal.timeout(30000),
    headers: { Origin: base.origin, "Content-Type": "application/json" },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  if (!response.ok)
    throw new Error(`Parsar returned ${response.status} for ${path}`);
  return response.json();
}
const read = async (name) =>
  JSON.parse(await readFile(new URL(name, import.meta.url), "utf8"));
const environment = await read("./environment.json"),
  agent = await read("./agent.json");
environment.setup_script = await readFile(
  new URL("./setup.sh", import.meta.url),
  "utf8",
);
const environmentId = randomUUID(),
  agentId = randomUUID();
agent.model_id = args.get("--model");
agent.skill_ids = args.get("--skills").split(",");
agent.runtime_id = environmentId;
agent.connection_requirement = {
  service: "feishu",
  bot_id: args.get("--app"),
  scopes: args.get("--scopes") || "contact:user.base:readonly",
};
await api(`/app/runtimes/${environmentId}`, "PUT", environment);
try {
  await api(`/app/agents/${agentId}`, "PUT", agent);
} catch (error) {
  await api(`/app/runtimes/${environmentId}`, "DELETE");
  throw error;
}
console.log(
  JSON.stringify({
    agent_id: agentId,
    environment_id: environmentId,
    agent_url: `${base.origin}/#/agents/${agentId}?view=full`,
  }),
);
