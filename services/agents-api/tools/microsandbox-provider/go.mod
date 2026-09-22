module github.com/MiniMax-AI-Dev/parsar/services/agents-api/tools/microsandbox-provider

go 1.25.13

require (
	github.com/MiniMax-AI-Dev/parsar v0.0.0
	github.com/superradcompany/microsandbox/sdk/go v0.7.2
)

require github.com/google/uuid v1.6.0 // indirect

// The helper ships from this repository; the SDK itself is a released module.
replace github.com/MiniMax-AI-Dev/parsar => ../../../..
