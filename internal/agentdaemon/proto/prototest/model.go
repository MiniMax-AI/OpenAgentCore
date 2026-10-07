package prototest

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Every Core request names a model and a provider, and Runtime preparation
// rejects a request without them. ModelConfiguration declares the fixture
// provider's protocol for a fixture Harness, and WithModel gives a request the
// fixture model and provider.

func ModelConfiguration() harnessconfig.Configuration {
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: string(modelprovider.Responses)}}}
}

func WithModel(req proto.PromptRequestPayload) proto.PromptRequestPayload {
	req.Model = "fixture-model"
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://model.example/v1", APIKey: "fixture-key"}
	return req
}
