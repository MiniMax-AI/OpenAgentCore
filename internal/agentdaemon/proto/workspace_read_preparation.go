package proto

// ValidWorkspaceReadPreparation excludes execution configuration.
// The native adapter supplies temporary state; this request cannot resume or start.
func ValidWorkspaceReadPreparation(r PromptRequestPayload) bool {
	return r.WorkspaceReadOnly && r.LocalEnvironment != nil && r.AgentStateKey != "" &&
		r.RunID == "" && len(r.Input) == 0 && r.AgentSessionID == "" &&
		!r.RequireExistingNativeSession && !r.DisableExecutionEnvironment &&
		r.Model == "" && r.SystemPrompt == "" && r.ModelProvider == nil && len(r.HarnessConfig) == 0 &&
		r.ExecutionControls == nil && r.MCPHTTPServers == nil &&
		len(r.FunctionTools) == 0 && !r.ToolSearch &&
		!r.ObserveSubagentIdentities
}
