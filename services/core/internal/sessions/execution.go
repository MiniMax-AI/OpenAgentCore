package sessions

import "errors"

// ExecutionOperations are the Session writes only the execution owner makes.
// Each runs in a Session transaction on the connection that holds the
// execution lease, so losing the lease fences it.
type ExecutionOperations struct{ storage ExecutionStorage }

// NewExecutionOperations binds the Session execution operations to the
// lease-bound storage of one execution owner.
func NewExecutionOperations(storage ExecutionStorage) (*ExecutionOperations, error) {
	if storage == nil {
		return nil, errors.New("session execution operations require execution storage")
	}
	return &ExecutionOperations{storage: storage}, nil
}

// ExecutionStorage is the lease-bound Session storage, one family per line.
type ExecutionStorage interface {
	EnvironmentExecution
	FunctionExecution
	JournalExecution
}
