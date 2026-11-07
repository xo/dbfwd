package dbfwd

import (
	"context"
	"fmt"

	"github.com/xo/dburl"
)

// Lister is a Backend that can list the databases it reaches (D18).
type Lister interface {
	// List lists the databases that u names.
	List(ctx context.Context, u *dburl.URL, cfg Config) (*Listing, error)
}

// Listing is what List found.
type Listing struct {
	// Scope is where List looked, as Target.Scope holds it, with the context
	// and the namespace resolved. With every namespace, the namespace is "*".
	Scope []Field

	// Entries holds one entry for each database, sorted by scope, then by
	// name.
	Entries []Entry

	// Skipped holds each part of the scope that List did not read, such as a
	// kind that RBAC does not allow.
	Skipped []Skip
}

// Entry is one database that List found.
type Entry struct {
	// Target is the object that the row stands for, as Lookup reports it.
	Target Target

	// Operator is the name of the operator that manages the database, such
	// as "CloudNativePG", or empty when none does.
	Operator string

	// URL is the shortest URL that Resolve maps to the same database, such
	// as "k://pg.prod/". It holds no password.
	URL string

	// Roles holds the roles that the database serves, such as "rw" and "ro".
	Roles []string

	// Ready is the number of pods that are ready, and Pods the number of
	// pods.
	Ready, Pods int

	// Databases holds the databases that the rules name, or none.
	Databases []string

	// Credential says where Resolve reads the password, such as
	// "Secret pg-app key password". It never holds a secret value.
	Credential string
}

// Skip is a part of the scope that List did not read.
type Skip struct {
	// Scope is where, such as the namespace "prod".
	Scope []Field

	// Kind is what List tried to read there, such as "services".
	Kind string

	// Reason says why, such as "not allowed".
	Reason string
}

// List lists the databases that u names. The name and the namespace in u can
// be "*", and the name can hold "*" as a wildcard, such as k://*, k://*.prod
// or k://pg*.prod. List reads no credential and starts no forward.
//
// List fails only when it read nothing, or when RBAC forbids the scope that u
// asks for. When it read some of the scope, it lists the rest in Skipped.
func List(ctx context.Context, u *dburl.URL, opts ...Option) (*Listing, error) {
	b, err := backendFor(u)
	if err != nil {
		return nil, err
	}
	l, ok := b.(Lister)
	if !ok {
		return nil, fmt.Errorf("listing the databases of %s: %w", u.SchemeName, ErrUnsupported)
	}
	return l.List(ctx, u, newConfig(opts))
}
