// Package writeaudit carries authenticated, non-secret request provenance to
// business transactions. It does not authorize requests or replace principals.
package writeaudit

import "context"

// Source contains no bearer token, payload, file path or native credentials.
// RequestID is server-generated; TraceID may be shared by related requests.
type Source struct {
	KeyID     string `json:"key_id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Kind      string `json:"kind"`
	TenantID  string `json:"tenant_id"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

type sourceKey struct{}

func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func FromContext(ctx context.Context) (Source, bool) {
	source, ok := ctx.Value(sourceKey{}).(Source)
	return source, ok
}
