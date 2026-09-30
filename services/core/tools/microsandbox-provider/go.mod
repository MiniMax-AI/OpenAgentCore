module github.com/MiniMax-AI/OpenAgentCore/services/core/tools/microsandbox-provider

go 1.26.8

require (
	github.com/MiniMax-AI/OpenAgentCore v0.0.0
	github.com/superradcompany/microsandbox/sdk/go v0.7.2
)

require (
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

// The helper ships from this repository; the SDK itself is a released module.
replace github.com/MiniMax-AI/OpenAgentCore => ../../../..
