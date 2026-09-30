package modelconfiguration

import "context"

// Storage keeps one deployment default per Harness. Replace and Delete record
// the administrator mutation in the same transaction, from the provenance the
// context carries; an audit failure aborts the change.
type Storage interface {
	// Replace stores record under a new revision and clears the observations
	// of the revision it replaces.
	Replace(ctx context.Context, record Record) (Configuration, error)
	// Delete removes the Harness's default. Removing a missing default succeeds
	// and is audited too.
	Delete(ctx context.Context, harness string) error
	// LoadSealed returns the Harness's sealed bundle and its revision, or
	// ErrNotFound.
	LoadSealed(ctx context.Context, harness string) (Sealed, error)
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
