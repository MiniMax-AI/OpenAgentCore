package sessions

// Reader reads a Session's resources, Environment and devices, and the
// administrator's cross-Project Session views, one family per line. Callers
// use it directly; no use case forwards a read.
type Reader interface {
	AdminReader
	ArtifactReader
	DeviceReader
	EnvironmentReader
	ExecutorCredentialReader
	ItemReader
	SubagentReader
}
