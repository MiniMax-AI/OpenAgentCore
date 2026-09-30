// Package adminaudit carries non-secret administrator provenance into business transactions.
package adminaudit

import "context"

type Source struct {
	CredentialID string
	ActorLabel   string
	RequestID    string
	TraceID      string
	ProjectID    string
}
type sourceKey struct{}

func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}
func FromContext(ctx context.Context) (Source, bool) {
	source, ok := ctx.Value(sourceKey{}).(Source)
	return source, ok
}
