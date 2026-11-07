// Package dbfwd connects a program to a database that is reachable only
// through a forward, such as a database that runs on Kubernetes.
//
// [Resolve] reads a URL that [github.com/xo/dburl] parsed, such as
// k+cnpg://pg.prod/app, finds the database and its credentials, starts the
// forward, and returns a [Forward] with an ordinary URL for a local port. The
// URL holds no password. The backend finds it.
//
// A backend registers itself from init. A program imports the backends it
// uses:
//
//	import _ "github.com/xo/dbfwd/k8s"
package dbfwd

import (
	"context"
	"fmt"
	"sync"

	"github.com/xo/dburl"
)

// registry holds the backends by scheme name.
var registry = struct {
	sync.RWMutex

	backends map[string]Backend
}{
	backends: make(map[string]Backend),
}

// Register makes a backend available for the scheme name, such as "k8s". It
// panics when the name is empty or taken, or the backend is nil. Call it from
// init.
func Register(name string, b Backend) {
	if name == "" || b == nil {
		panic("dbfwd: Register needs a name and a backend")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, ok := registry.backends[name]; ok {
		panic("dbfwd: Register called twice for " + name)
	}
	registry.backends[name] = b
}

// backendFor returns the backend for the scheme of u.
func backendFor(u *dburl.URL) (Backend, error) {
	if u == nil {
		return nil, ErrNoBackend
	}
	registry.RLock()
	defer registry.RUnlock()
	b, ok := registry.backends[u.SchemeName]
	if !ok {
		return nil, fmt.Errorf("finding a backend for %q: %w", u.SchemeName, ErrNoBackend)
	}
	return b, nil
}

// Handles reports whether a backend is registered for the scheme of u. A
// caller, such as usql, uses it in place of a list of schemes of its own.
func Handles(u *dburl.URL) bool {
	_, err := backendFor(u)
	return err == nil
}

// Resolve finds the database that u names, starts a forward to it, and
// returns the Forward. It returns when the forward is ready.
//
// The backend is chosen by u.SchemeName. Canceling ctx before Resolve returns
// stops it and releases everything it started. After Resolve returns, ctx no
// longer controls the forward, and [Forward.Close] does.
func Resolve(ctx context.Context, u *dburl.URL, opts ...Option) (*Forward, error) {
	b, err := backendFor(u)
	if err != nil {
		return nil, err
	}
	o, err := b.Open(ctx, u, newConfig(opts))
	if err != nil {
		return nil, err
	}
	f, err := newForward(o)
	if err != nil {
		_ = o.Tunnel.Close()
		return nil, err
	}
	return f, nil
}

// Lookup runs the lookup for u and reports what matched, with each step it
// tried. It reads no credential and starts no forward.
func Lookup(ctx context.Context, u *dburl.URL, opts ...Option) (*Match, error) {
	b, err := backendFor(u)
	if err != nil {
		return nil, err
	}
	return b.Lookup(ctx, u, newConfig(opts))
}
