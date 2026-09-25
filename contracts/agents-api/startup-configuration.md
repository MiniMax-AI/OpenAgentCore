# Core startup configuration (removed)

The startup configuration read is removed, with no replacement route. Neither
`GET /core/v1/admin/startup-configuration` nor the earlier
`GET /v1/agents/core/startup-configuration` exists; `/v1` serves only the pinned
upstream routes and `/core/v1` has no equivalent. A Session's committed harness
and model selection remain available through the administrator
[execution configuration](execution-configuration.md) read.
