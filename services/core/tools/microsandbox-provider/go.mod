module github.com/MiniMax-AI/OpenAgentCore/services/core/tools/microsandbox-provider

go 1.26.8

require (
	github.com/MiniMax-AI/OpenAgentCore v0.0.0
	github.com/google/uuid v1.6.0
	github.com/superradcompany/microsandbox/sdk/go v0.0.0-20261024213825-df3e0f7dd692
)

require golang.org/x/sync v0.22.0 // indirect

// The helper ships from this repository.
replace github.com/MiniMax-AI/OpenAgentCore => ../../../..

// The SDK is upstream v0.7.11 plus the fork's patch queue (docs/maintainers.md).
replace github.com/superradcompany/microsandbox/sdk/go => github.com/SaladDay/microsandbox/sdk/go v0.0.0-20261028065500-fc39b5b503f0
