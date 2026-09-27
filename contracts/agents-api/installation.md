# Installation facts

`GET /core/v1/installation` reports what an administrator needs to call and
change this installation. Core key only, like every `/core/v1` route; Web forwards
it after sign-in. It is available before any sandbox deployment exists and makes no
provider or model call. See the [Core OpenAPI](core.openapi.yaml) for the schema.

| Field | Source |
| --- | --- |
| `object` | Always `core.installation` |
| `installation_id` | `OAC_INSTALLATION_ID`; null when Core runs without the sandbox manager |
| `public_url` | `OAC_PUBLIC_URL`: the origin applications, nodes, sandbox guests and self-hosted executors use. Null when unset |
| `api_base_url` | `public_url` followed by `/v1`: the base URL for Project API keys (`OPENAI_BASE_URL`). Null when `public_url` is null |
| `local_only` | True when `public_url` names a loopback host, which only the Core host reaches |
| `source_commit` | The full source commit Core was built from; null for development builds |
| `configuration` | The installer's settings snapshot (`OAC_SETTINGS_FILE`); null when the installer did not start Core |
| `address_bindings` | What a change of `public_url` affects, counted on each read |

`configuration` has:

- `path`: the absolute host path of the installation's `config.json`, where every
  process setting is changed;
- `apply_command`: the command that applies `config.json` changes;
- `applied_at`: when the snapshot was last applied;
- `settings`: one item per `config.json` setting, with its dotted `key`, applied
  `value`, `default`, whether it is `changeable` after installation, whether it is
  `sensitive`, and the services it `restarts` (`core`, `web`, `database`).

A sensitive setting always has a null `value` and `default`, and a boolean
`configured` instead; only sensitive settings have `configured`. Core refuses to
start when the snapshot breaks this rule, has a duplicate key or has an unknown
member. Core reports the snapshot and never acts on it; runtime settings, such as
the sandbox deployment, have their own routes.

`address_bindings` has:

| Field | Meaning |
| --- | --- |
| `nodes` | Enrolled nodes that are not removed |
| `nodes_on_other_address` | Nodes enrolled with an address other than `public_url`. They receive no new sandboxes; remove and add them again. At most `nodes` |
| `hosted_sandboxes` | Retained and pending hosted sandboxes, started with the address current at the time |
| `self_hosted_executors` | Unrevoked executor credentials; their executors were installed with the advertised `remote_url` |
