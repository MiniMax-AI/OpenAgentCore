module github.com/MiniMax-AI/OpenAgentCore/services/core/tools/microsandbox-provider

go 1.26.8

require (
	github.com/MiniMax-AI/OpenAgentCore v0.0.0
	github.com/superradcompany/microsandbox/sdk/go v0.0.0-20261009155513-7b7b9dc89e9a
)

require (
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

// The helper ships from this repository; the SDK itself is a released module.
replace github.com/MiniMax-AI/OpenAgentCore => ../../../..
