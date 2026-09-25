# Node enrollment and capacity confirmation

This is a deployment-administrator extension under `/core/v1/sandbox`, not part
of the public Agents API. The paired console proxies these routes after login;
its private deployment credential never reaches the browser. Project, enrollment
and node credentials cannot call administrator routes.

## Administrator workflow

1. Create an enrollment token, retain the response's opaque `id`, and run the
   matched installation command on the target Linux host.
2. Poll that receipt, not the newest node or list length. On `enrolled`, use its
   `node_id` to read the exact node. Connection does not authorize scheduling.
3. Show the observed host resources, deployment-owned sandbox specification,
   reserve and Core-calculated recommendation. Choose a positive active-sandbox
   limit no greater than `selectable_max_active`; an optional name is editable.
4. Submit the explicit enable PATCH with the current configuration revision.
   Show success only after a confirmed response or a subsequent matching GET.

Closing the installation UI does not undo enrollment. Pending nodes remain in
node listings and can be confirmed later. Reinstall and reconnect preserve node
identity and administrator settings. An enrolled receipt remains enrolled after
its original token expires. Do not repeat writes automatically after a network
failure; retrieve the receipt or node first.

The backend supports this flow; page layout and frontend integration are owned
separately. Do not infer installation progress before an authenticated connection.
Local installation errors are printed in the host terminal.

## Routes and shapes

| Method | Path below `/core/v1/sandbox` | Result |
| --- | --- | --- |
| POST | `/enrollment-tokens` | 201 `{id, token, expires_at}`; body `{}` |
| GET | `/enrollment-tokens/{id}` | `{id, status, node_id, expires_at}`; never the token |
| GET | `/nodes` | `{data: Node[]}` |
| GET | `/nodes/{id}` | Node |
| PATCH | `/nodes/{id}` | Updated Node |
| DELETE | `/nodes/{id}` | Existing removal; requires no retained resources |

Receipt `status` is `waiting`, `enrolled` or `expired`. `node_id` is null before
successful enrollment. Concurrent installations have distinct receipts.

Node adds:

- `admission_state`: `pending_confirmation` or `enabled`.
- `schedulable`: effective current admission, including deployment maintenance,
  provider readiness, connectivity, matching specification and remaining capacity.
- `config_revision`: opaque node configuration plus relevant deployment identity.
  Ordinary heartbeats do not change it.
- `max_active`, `max_retained`: null before confirmation; saved integers afterward.
- `host`: `effective_cpu_cores`, `total_memory_bytes`, `effective_memory_bytes`,
  `available_memory_bytes`, `available_disk_bytes`, `observed_at`. Missing values
  are null, never zero placeholders.
- `sandbox_spec`: `cpu_cores`, `memory_bytes`, `root_disk_limit_bytes`,
  `environment_disk_limit_bytes`, `runtime_version`. Disk limits are null where
  unsupported; Runtime version is the immutable distribution source commit.
- `capacity`: `status` (`checking`, `ready`, `blocked`),
  `recommended_max_active`, `selectable_max_active`, `reserved_cpu_cores`,
  `reserved_memory_bytes`, and machine-readable `reasons`.

Capacity reasons are `specification_unavailable`, `specification_mismatch`,
`deployment_maintenance`, `node_unavailable`, `observation_stale`,
`host_capacity_unknown`, `insufficient_host_capacity`, `available_memory_unknown`,
`insufficient_available_memory`, and the advisory `low_observed_disk_space`.
Check `status` and the saved admission state alongside reasons; low free memory
can prevent a new activation or increase without invalidating an enabled node's
saved limits. A name-only edit does not require new capacity.

Existing counts and connectivity fields remain available. `enabled` is saved
administrator intent; an offline or full enabled node is not pending confirmation.
A pending node cannot receive automatic or explicit placement. Enrollment payloads
and node heartbeats cannot enable it or set its scheduling limits.

```json
{
  "name": "worker-01",
  "max_active": 2,
  "admission_state": "enabled",
  "expected_config_revision": "revision-from-node-get"
}
```

PATCH is partial: omitted fields are preserved and explicit null is rejected.
First enable requires `max_active` and `admission_state: enabled`. Core supplies
`max_retained = max(max_active, 16)` unless explicitly supplied; subsequent omitted
retained capacity stays unchanged. Retained capacity includes snapshots and is not
extra concurrent execution capacity. Keep it out of the primary onboarding form.

Name-only updates can work while offline. First activation and capacity increases
require current compatible observations. Capacity cannot be reduced below retained
reservations. A different stale revision returns 409; an exact successful retry
returns the saved result while the relevant deployment remains unchanged.

## Resource calculation and limits

CPU reserve is **1 core**. Memory reserve is **max(2 GiB, 10% of effective memory)**.
The recommendation is the smaller whole-number CPU and memory capacity after
these reserves, divided by the deployment's enforced per-sandbox requirements.
Do not round an insufficient result up to one. CPU units are cores; memory and
disk values in this response are bytes. The deployment's resource input uses MiB.

Linux observations account for process CPU affinity and observable inherited
cgroup CPU/memory limits, capped by host memory. They describe the node process's
observable limits, not a reservation or certification of Docker daemon capacity.
Current available memory and disk are observations rather than guaranteed future
availability. Unrelated host workloads can still consume resources. Missing or
stale observations block a new confirmation; they do not silently resize an
already enabled node or interrupt its existing execution.

Disk is not divided into a slot budget without an enforced per-sandbox quota.
Free disk from the node state filesystem is only an observation: it may not be
the Provider storage filesystem, so low space is a warning rather than a claimed
provider-wide capacity guarantee. Docker's ordinary volume
size is not a disk quota. No dynamic scaling, overcommit setting, node-specific
sandbox profile, additional scheduler, or E2B node installation is introduced.
Existing node limits survive upgrade; this is persisted state, not another
execution architecture.

## Errors and recovery

Use the existing Core error envelope. Invalid fields return 400; missing objects
404; conflicting revision or unconfirmable capacity returns 409 with
`runtime_node_configuration_conflict`. Retrieve node detail to display its reasons
and retry only after deliberate confirmation. Existing authentication and database
availability errors remain unchanged. Never substitute a Project key on an admin
failure or treat failed observations as successful empty responses.
