package dbfwd_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/xo/dbfwd"
)

func TestErrorIs(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("forwarding a port: %w", dbfwd.ErrNotSupported)
	if !errors.Is(err, dbfwd.ErrNotSupported) {
		t.Fatalf("errors.Is(%v, ErrNotSupported) = false, expected true", err)
	}
	if got, want := dbfwd.ErrNotSupported.Error(), "not supported"; got != want {
		t.Fatalf("ErrNotSupported.Error() = %q, expected %q", got, want)
	}
}
