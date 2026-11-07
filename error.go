package dbfwd

// Error is an error.
type Error string

// Error satisfies the error interface.
func (err Error) Error() string {
	return string(err)
}

// Error values.
const (
	// ErrNotSupported is the not supported error.
	ErrNotSupported Error = "not supported"
)
