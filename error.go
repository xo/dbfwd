package dbfwd

import (
	"fmt"
	"strings"
)

// Error is an error.
type Error string

// Error satisfies the error interface.
func (err Error) Error() string {
	return string(err)
}

// Error values.
const (
	// ErrNoBackend is the error for a URL whose scheme has no backend.
	ErrNoBackend Error = "no backend for the scheme"
	// ErrNoTool is the error when a tool that the backend runs, such as
	// kubectl, is missing or cannot run. The error that wraps it names the
	// tool.
	ErrNoTool Error = "tool is missing or cannot run"
	// ErrUnauthorized is the error when the credentials of the person were
	// refused, because they expired or a login failed. The fix is to log in
	// again.
	ErrUnauthorized Error = "not logged in"
	// ErrForbidden is the error when a step was not allowed, by RBAC or by
	// IAM. The fix is to ask for access.
	ErrForbidden Error = "not allowed"
	// ErrNotFound is the error when no step of the lookup matched.
	ErrNotFound Error = "not found"
	// ErrAmbiguous is the error when a step matched more than one object, or
	// the signals for the type disagree.
	ErrAmbiguous Error = "more than one match"
	// ErrUnsupported is the error for a database that no database/sql driver
	// can reach, such as MongoDB, or for a target that a forward cannot reach,
	// such as an ExternalName Service.
	ErrUnsupported Error = "not supported"
	// ErrRoleConflict is the error when role disagrees with the target, such
	// as role=ro with the Service pg-rw.
	ErrRoleConflict Error = "role conflicts with the target"
	// ErrQueryKey is the error when a driver key has the name of a key of the
	// backend.
	ErrQueryKey Error = "query key clashes with a key of the backend"
	// ErrBadURL is the error when a URL cannot be read. It names no part of
	// the URL, because a local URL holds a password.
	ErrBadURL Error = "the URL is not valid"
	// ErrForwardLost is the error when a running forward stops on its own.
	ErrForwardLost Error = "forward lost"
	// ErrClosed is the error when a forward is used after Close.
	ErrClosed Error = "forward closed"
)

// LookupError is the error of a lookup that failed. It names what was tried,
// and wraps one of the error values.
type LookupError struct {
	// Name is the name in the URL.
	Name string

	// Scope is where the lookup looked.
	Scope []Field

	// Steps is each step that was tried.
	Steps []Step

	// Err is the error value, such as ErrNotFound.
	Err error
}

// Error satisfies the error interface.
func (err *LookupError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "finding %s", err.Name)
	for i, f := range err.Scope {
		if f.Value == "" {
			continue
		}
		if i == 0 {
			b.WriteString(" in ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(f.Name + " " + f.Value)
	}
	if len(err.Steps) != 0 {
		b.WriteString(" (tried")
		for i, s := range err.Steps {
			if i != 0 {
				b.WriteString(",")
			}
			b.WriteString(" " + s.Kind + ": " + s.Result)
		}
		b.WriteString(")")
	}
	b.WriteString(": ")
	b.WriteString(err.Err.Error())
	return b.String()
}

// Unwrap returns the error value.
func (err *LookupError) Unwrap() error {
	return err.Err
}
