package sessions

// Reader reads Sessions and their resources, Environment and devices, and the
// administrator's cross-Project Session views, one family per line. Callers
// use it directly; no use case forwards a read.
type Reader interface {
	AdminReader
	ArtifactReader
	DeviceReader
	EnvironmentReader
	ExecutorCredentialReader
	InputReader
	ItemReader
	ModelExecutionReader
	SessionReader
	SubagentReader
	TurnReader
}
