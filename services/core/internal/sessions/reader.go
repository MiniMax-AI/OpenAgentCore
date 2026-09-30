package sessions

// Reader reads a Session's resources, Environment and devices, one family per
// line. Callers use it directly; no use case forwards a read.
type Reader interface {
	ArtifactReader
	DeviceReader
	EnvironmentReader
	ExecutorCredentialReader
	ItemReader
	SubagentReader
}
