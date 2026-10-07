package dispatch

// localDirectoryPreparation is a ready read-only preparation. The bound local
// workspace serves its reads, so it holds no native resource.
type localDirectoryPreparation struct{}

func (localDirectoryPreparation) Close() error { return nil }
