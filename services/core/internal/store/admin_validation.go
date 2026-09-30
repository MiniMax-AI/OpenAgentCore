package store

// AdminValidationError adds field identity without changing the store error text
// or ErrInvalidInput classification. Bounds are fixed validator constants.
type AdminValidationError struct {
	Code, Param string
	MaxLength   int
	message     string
}

func (e *AdminValidationError) Error() string { return e.message }
func (e *AdminValidationError) Unwrap() error { return ErrInvalidInput }
