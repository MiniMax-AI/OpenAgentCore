package main

import "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"

func managedRuntimeProviderKind(runtime *execution.RuntimeProvider) string {
	if runtime == nil {
		return ""
	}
	return runtime.ProviderKind
}
