# Core startup configuration (removed)

The startup configuration read is removed. The separate
[installation read](installation.md), `GET /core/v1/installation`, reports the public
URL and the installer's process settings. Neither
`GET /core/v1/admin/startup-configuration` nor the earlier
`GET /v1/agents/core/startup-configuration` exists; `/v1` serves only the pinned
upstream routes and `/core/v1` has no startup-configuration route. A Session's committed harness
and model selection remain available through the administrator
[execution configuration](execution-configuration.md) read.
