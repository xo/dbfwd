package dbfwd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xo/dburl"
)

// Backend finds a database and starts a forward to it. A backend registers
// itself from init with [Register].
type Backend interface {
	// Lookup runs the lookup for u.
	Lookup(ctx context.Context, u *dburl.URL, cfg Config) (*Match, error)

	// Open starts a forward to the database that u names, and returns once it
	// is ready.
	Open(ctx context.Context, u *dburl.URL, cfg Config) (*Opened, error)
}

// Tunnel is the running forward of a backend.
type Tunnel interface {
	// Done returns a channel that is closed when the tunnel stops.
	Done() <-chan struct{}

	// Err returns why the tunnel stopped, after Done is closed.
	Err() error

	// Close stops the tunnel. It is safe to call more than once.
	Close() error
}

// Opened is what a backend returns from Open.
type Opened struct {
	// URL is the URL of the local end, as a string for dburl.Parse. It holds
	// the password.
	URL string

	// Expires is when the credential in URL expires. It is zero when the
	// credential does not expire.
	Expires time.Time

	// Renew makes a new URL when the credential expires, and returns when the
	// new one expires. It is nil when the credential does not expire.
	Renew func(ctx context.Context) (string, time.Time, error)

	// User is the user the URL logs in as.
	User string

	// Database is the database the URL opens, or empty.
	Database string

	// Target is the object that the lookup matched.
	Target Target

	// Tunnel is the running forward.
	Tunnel Tunnel
}

// Target is the object that a lookup matched.
type Target struct {
	// Backend is the name of the backend, such as "k8s".
	Backend string

	// Scope is where the object lives, from the outside in, such as the
	// context and the namespace for Kubernetes.
	Scope []Field

	// Kind is the kind of the object that matched, such as "Service".
	Kind string

	// Name is the name of the object that matched, such as "pg-rw".
	Name string

	// Owner is what manages the object, such as "CloudNativePG Cluster pg".
	// It is empty when nothing manages it.
	Owner string

	// Type is the database type, such as "postgres".
	Type string
}

// String returns the target, such as "Service pg-rw in context kind-prod,
// namespace prod, owned by CloudNativePG Cluster pg".
func (t Target) String() string {
	var b strings.Builder
	b.WriteString(t.Kind)
	if t.Name != "" {
		b.WriteString(" " + t.Name)
	}
	for i, f := range t.Scope {
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
	if t.Owner != "" {
		b.WriteString(", owned by " + t.Owner)
	}
	return b.String()
}

// Field is one named value of a Scope.
type Field struct {
	Name  string
	Value string
}

// Match is what Lookup found.
type Match struct {
	// Target is the object that matched.
	Target Target

	// Steps is each step that Lookup tried, in order.
	Steps []Step
}

// Step is one step of a lookup.
type Step struct {
	// Kind is the kind that the step looked for, such as "Helm release".
	Kind string

	// Result says what the step found, such as "no match", "not installed",
	// "not allowed" or "match".
	Result string
}

// renewMargin is how long before it expires that a credential is renewed.
const renewMargin = time.Minute

// Forward is a running forward to a database. Its methods are safe to call
// from more than one goroutine.
type Forward struct {
	// User is the user the URL logs in as.
	User string

	// Database is the database the URL opens. It is empty when the database
	// has none.
	Database string

	// Target is the object that the lookup matched.
	Target Target

	tunnel Tunnel
	closed atomic.Bool

	mu      sync.Mutex
	url     string
	expires time.Time
	renew   func(ctx context.Context) (string, time.Time, error)
}

// newForward builds the Forward around what a backend opened.
func newForward(o *Opened) (*Forward, error) {
	if o == nil || o.Tunnel == nil {
		return nil, fmt.Errorf("opening a forward: %w", ErrForwardLost)
	}
	if _, err := parse(o.URL); err != nil {
		return nil, err
	}
	return &Forward{
		User:     o.User,
		Database: o.Database,
		Target:   o.Target,
		tunnel:   o.Tunnel,
		url:      o.URL,
		expires:  o.Expires,
		renew:    o.Renew,
	}, nil
}

// parse parses a local URL. Its error never holds the URL, because the URL
// holds the password.
func parse(s string) (*dburl.URL, error) {
	u, err := dburl.Parse(s)
	if err != nil {
		return nil, ErrBadURL
	}
	return u, nil
}

// URL returns the URL of the local end, such as
// postgres://app:...@127.0.0.1:41234/app. It holds the password, so it must
// never be printed or logged. When the credential of the backend expires, URL
// makes a new one first. After the forward is lost, it returns an error that
// wraps ErrForwardLost.
//
// Call URL for each new connection, and do not keep its result. It is cheap
// when nothing expired, and safe to call from many goroutines at once.
func (f *Forward) URL(ctx context.Context) (*dburl.URL, error) {
	if err := f.stopped(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renew != nil && !f.expires.IsZero() && time.Now().Add(renewMargin).After(f.expires) {
		s, expires, err := f.renew(ctx)
		if err != nil {
			return nil, fmt.Errorf("renewing the credential for %s: %w", f.Target, err)
		}
		f.url, f.expires = s, expires
	}
	return parse(f.url)
}

// Done returns a channel that is closed when the forward stops, by Close or
// because it was lost.
func (f *Forward) Done() <-chan struct{} {
	return f.tunnel.Done()
}

// Err returns nil while the forward runs and after Close. It returns why the
// forward was lost, as an error that wraps ErrForwardLost.
func (f *Forward) Err() error {
	if f.closed.Load() {
		return nil
	}
	select {
	case <-f.tunnel.Done():
	default:
		return nil
	}
	if f.closed.Load() {
		return nil
	}
	if err := f.tunnel.Err(); err != nil {
		return fmt.Errorf("forward to %s: %w: %w", f.Target, ErrForwardLost, err)
	}
	return fmt.Errorf("forward to %s: %w", f.Target, ErrForwardLost)
}

// Close stops the forward and releases the local port. It is safe to call
// more than once.
func (f *Forward) Close() error {
	if f.closed.Swap(true) {
		return nil
	}
	return f.tunnel.Close()
}

// String returns the target, such as "Service pg-rw in context kind-prod,
// namespace prod, owned by CloudNativePG Cluster pg". It never holds the
// password.
func (f *Forward) String() string {
	return f.Target.String()
}

// GoString returns the same as String, so that %#v holds no password.
func (f *Forward) GoString() string {
	return f.String()
}

// LogValue returns the target for log/slog. It never holds the password.
func (f *Forward) LogValue() slog.Value {
	return slog.StringValue(f.String())
}

// stopped returns an error when the forward was closed or lost.
func (f *Forward) stopped() error {
	if f.closed.Load() {
		return fmt.Errorf("using the forward to %s: %w", f.Target, ErrClosed)
	}
	return f.Err()
}
