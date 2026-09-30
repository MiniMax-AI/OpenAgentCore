package sessions

// Reader reads a Session's public resources, one family per line. Callers use
// it directly; no use case forwards a read.
type Reader interface {
	ArtifactReader
	ItemReader
	SubagentReader
}
