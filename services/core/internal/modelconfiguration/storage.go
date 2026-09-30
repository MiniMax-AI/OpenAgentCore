package modelconfiguration

import "context"

// Storage keeps one deployment default per Harness and seals and opens its
// bundle; without a credential key, Replace and LoadBundle return
// credentialcrypto.ErrUnavailable. Replace and Delete record the administrator
// mutation in the same transaction, from the provenance the context carries;
// an audit failure aborts the change.
type Storage interface {
	// Replace seals and stores record under a new revision and clears the
	// observations of the revision it replaces.
	Replace(ctx context.Context, record Record) (Configuration, error)
	// Delete removes the Harness's default. Removing a missing default succeeds
	// and is audited too.
	Delete(ctx context.Context, harness string) error
	// LoadBundle opens the Harness's bundle and returns it with its revision.
	// A Harness without a default is ErrNotFound, checked before the key.
	LoadBundle(ctx context.Context, harness string) (Bundle, error)
}

// Reader lists the configured defaults without opening any bundle.
type Reader interface {
	List(ctx context.Context) ([]Configuration, error)
}

// Observer records a committed root Turn's outcome against the deployment
// default revision its Session froze. It runs after the Turn's commit, outside
// the execution lease, and the statement itself re-checks the committed
// outcome, the tenant, the provider source and the exact revision. It returns
// the number of defaults updated: zero when the Turn does not qualify, the
// default was replaced or removed, or the throttle skips the update.
type Observer interface {
	ObserveDeploymentModelProvider(ctx context.Context, observation Observation) (int64, error)
}
