module github.com/MiniMax-AI/OpenAgentCore/services/core/tools/microsandbox-provider

go 1.26.8

require (
	github.com/MiniMax-AI/OpenAgentCore v0.0.0
	github.com/superradcompany/microsandbox/sdk/go v0.7.2
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/libp2p/go-buffer-pool v0.0.2 // indirect
	github.com/libp2p/go-yamux/v5 v5.1.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// The helper ships from this repository; the SDK itself is a released module.
replace github.com/MiniMax-AI/OpenAgentCore => ../../../..
