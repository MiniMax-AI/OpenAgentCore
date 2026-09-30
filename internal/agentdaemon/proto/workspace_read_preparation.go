package proto

// ValidWorkspaceReadPreparation excludes execution configuration.
// The native adapter supplies temporary state; this request cannot resume or start.
func ValidWorkspaceReadPreparation(r PromptRequestPayload) bool {
	return r.WorkspaceReadOnly && r.LocalEnvironment != nil && r.AgentStateKey != "" &&
		r.StrictResume && r.ReleaseOnCompletion && r.RunID == "" && len(r.Input) == 0 &&
		r.ConversationID == "" && r.AgentSessionID == "" &&
		!r.RequireExistingNativeSession && !r.WorkspaceAuthoring && !r.DisableExecutionEnvironment &&
		len(r.AgentOptions) == 0 && r.ExecutionControls == nil && r.MCPHTTPServers == nil &&
		len(r.FunctionTools) == 0 && !r.ToolSearch && !r.ObserveMessages &&
		!r.ObserveToolObservations && !r.ObserveSubagentIdentities
}
