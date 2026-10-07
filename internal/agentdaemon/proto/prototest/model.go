package prototest

import (
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Every Core request names a model and a provider, and Runtime preparation
// rejects a request without them. ModelConfiguration declares the fixture
// provider's protocol and full support for a fixture Harness, so each fixture's
// advertised capabilities narrow it, and WithModel gives a request the fixture
// model and provider.

func ModelConfiguration() harnessconfig.Configuration {
	supported := proto.CapabilitySupported
	var capabilities proto.AgentKindCapabilities
	fields := reflect.ValueOf(&capabilities).Elem()
	for i := 0; i < fields.NumField(); i++ {
		fields.Field(i).Set(reflect.ValueOf(supported))
	}
	return harnessconfig.Configuration{Providers: []harnessconfig.Provider{{Protocol: string(modelprovider.Responses)}}, Declaration: proto.Declaration{
		Capabilities: capabilities, WhitespaceOnlyText: supported, FunctionResultImageURLs: supported, FailedFunctionResultImages: supported,
		MCPAllowedTools: supported, MCPOrigins: []string{"service", "environment"}}}
}

func WithModel(req proto.PromptRequestPayload) proto.PromptRequestPayload {
	req.Model = "fixture-model"
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://model.example/v1", APIKey: "fixture-key"}
	return req
}
