# The API

This document holds the Go API of the root package `dbfwd` and the shape of
the `k8s` backend. D15 holds the decision, and the code follows this
document. `go doc` is the reference for each name.

The API follows D2, D3, D7, D10 and D11.
Kubernetes is the only backend now. The root package names nothing that only
Kubernetes has, so that the future backends in `docs/PLAN.md` fit with no
change to a caller.

## The root package

```go
package dbfwd

// Resolve finds the database that u names, starts a forward to it, and
// returns the Forward. It returns when the forward is ready.
//
// The backend is chosen by u.SchemeName. Canceling ctx before Resolve
// returns stops it and releases everything it started. After Resolve
// returns, ctx no longer controls the forward, and Close does.
func Resolve(ctx context.Context, u *dburl.URL, opts ...Option) (*Forward, error)

// Lookup runs the lookup for u and reports what matched, with each step it
// tried. It reads no credential and starts no forward. The dbfwd command
// uses it.
func Lookup(ctx context.Context, u *dburl.URL, opts ...Option) (*Match, error)

// Handles reports whether a backend is registered for the scheme of u. A
// caller, such as usql, uses it in place of a list of schemes of its own.
func Handles(u *dburl.URL) bool
```

`Resolve` returns when the forward is ready, so a caller can connect at once.
A context that is canceled after that does not stop the forward, because
`usql` resolves with a context that ends when the command ends, and holds the
`Forward` for the whole session.

### Options

The options follow the functional options pattern. A call with no options is
ready to use. No option is tied to one backend.

```go
// Option sets one value of the Config of a call of Resolve or Lookup.
type Option func(*Config)

// WithTool runs the tool name from path, such as WithTool("kubectl",
// "/opt/bin/kubectl"). With no such option, a backend looks for the tool on
// the PATH.
func WithTool(name, path string) Option

// WithEnv sets the environment variable key to value for every child process
// of the call, such as WithEnv("KUBECONFIG", path) or
// WithEnv("AWS_PROFILE", "prod"). With no such option, a child process gets
// the environment of the caller.
func WithEnv(key, value string) Option

// WithInteractive runs each child process that can prompt inside fn, so that
// a credential plugin or a login can use the terminal. With no such option,
// no child process gets a terminal.
func WithInteractive(fn func(run func(tty *os.File) error) error) Option

// Config is the configuration of one call, built from its options. A caller
// never builds one. A backend reads it.
type Config struct {
	// Tools maps the name of a tool to its path.
	Tools map[string]string

	// Env holds the variables that WithEnv set, as KEY=value.
	Env []string

	// Interactive runs a child process with the terminal, or is nil.
	Interactive func(run func(tty *os.File) error) error
}
```

`Resolve` builds a new `Config` for each call and applies the options in
order, so a later option wins. The caller owns the options, and no package
level value holds configuration (hard rule 3).

A kubeconfig is `WithEnv("KUBECONFIG", path)`, and a profile of AWS later is
`WithEnv("AWS_PROFILE", name)`. Each tool already reads its own variables, so
the root package needs no option for any one of them.

`WithInteractive` takes the shape of `Session.Release` in `rline`, so `usql`
passes `dbfwd.WithInteractive(l.Release)`. A backend runs the first call of
its tool inside it, such as the first `kubectl get`. That call gets the
terminal as its stdin and stderr, and the backend still reads its stdout. The
long running tunnel, such as `kubectl port-forward`, never runs inside it
(D11). The same rule fits `aws sso login` and `gcloud auth` later.

### Forward

```go
// Forward is a running forward to a database. Its methods are safe to call
// from more than one goroutine.
type Forward struct {
	// User is the user the URL logs in as.
	User string

	// Database is the database the URL opens. It is empty when the database
	// has none, as for MOCO with no database in the URL.
	Database string

	// Target is the object that the lookup matched.
	Target Target

	// unexported: the running tunnel, the credential and their state
}

// URL returns the URL of the local end, such as
// postgres://app:...@127.0.0.1:41234/app. It holds the password, so it must
// never be printed or logged. When the credential of the backend expires, URL
// makes a new one first. After the forward is lost, it returns an error that
// wraps ErrForwardLost.
//
// Call URL for each new connection, and do not keep its result. It is cheap
// when nothing expired, and safe to call from many goroutines at once.
func (f *Forward) URL(ctx context.Context) (*dburl.URL, error)

// Done returns a channel that is closed when the forward stops, by Close or
// because it was lost. A pod that goes does not close it while the forward
// can reconnect (D16).
func (f *Forward) Done() <-chan struct{}

// Err returns nil while the forward runs and after Close. It returns why the
// forward was lost, as an error that wraps ErrForwardLost.
func (f *Forward) Err() error

// Close stops the forward and releases the local port. It is safe to call
// more than once. On Linux, when the process dies with no Close, such as by
// SIGKILL, the k8s backend stops its kubectl too (D17).
func (f *Forward) Close() error

// String returns the target, such as "Service pg-rw in prod, owned by
// CloudNativePG Cluster pg". It never holds the password.
func (f *Forward) String() string

// GoString returns the same as String, so that %#v holds no password.
func (f *Forward) GoString() string

// LogValue returns the target for log/slog. It never holds the password.
func (f *Forward) LogValue() slog.Value
```

`URL` is a method and not a field, for two reasons. The password stays out of
every field, so that printing the struct in any form shows none. And a
credential can expire: an RDS IAM token lasts 15 minutes. For Kubernetes,
`URL` returns the same URL every time. A backend with a credential that
expires makes a new one when `URL` is called.

`String`, `GoString` and `LogValue` cover `%v`, `%s` and `%#v` in `fmt`, and
`log/slog`, so printing or logging a `Forward` by mistake shows no password.

`usql` opens its pool with `sql.OpenDB` and a `driver.Connector` of its own,
whose `Connect` calls `URL` and then opens the driver with that URL. So every
new connection that `database/sql` opens asks `dbfwd` for the URL, and an
expired credential is renewed with no retry logic in `usql`. That is why
`URL` must be cheap when nothing expired, safe for many goroutines, and fail
with `ErrForwardLost` once the forward is lost. A `Forward` keeps the current
URL, and only one goroutine renews it at a time. The connector is `usql`'s
own, and `dbfwd` is still not a driver (D2).

`URL` builds its result with `dburl.Parse` of a string, and never field by
field or from a value made at init. A caller such as `usql` changes the
registry of `dburl` from its own `init`, and a URL made before that can name a
driver that the caller does not register.

`Err` returns nil after `Close`, so a caller tells a lost forward from a
closed one.

A `Forward` watches the pod that it forwards to. When the pod goes, stops
being ready, or loses its role after a failover, the `Forward` picks a pod by
the same rule and starts the tunnel again on the same local port, so the URL
stays the same. A connection that was open through the old pod breaks. The
forward is lost only when no pod runs for two minutes (D16).

### Target and Match

```go
// Target is the object that the lookup matched.
type Target struct {
	// Backend is the name of the backend, such as "k8s".
	Backend string

	// Scope is where the object lives, from the outside in, such as
	// [{context kind-prod} {namespace prod}] for Kubernetes.
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

// Step is one step of the lookup.
type Step struct {
	// Kind is the kind that the step looked for, such as "Helm release".
	Kind string

	// Result says what the step found, such as "no match", "not installed"
	// or "not allowed".
	Result string
}
```

`Scope` holds the context and the namespace for Kubernetes. A later backend
fills it with what it has, such as an account and a region for AWS, or a
project and a region for Cloud SQL. `dbfwd lookup` prints the `Steps`, and
`usql` shows the `Target` in `\conninfo`.

### Errors

```go
// Error is an error.
type Error string

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

func (err *LookupError) Error() string
func (err *LookupError) Unwrap() error
```

A caller tells the cases apart with `errors.Is`, as in
`errors.Is(err, dbfwd.ErrForbidden)`, and reads the steps with `errors.As`.
`ErrNoTool` is the first failure that most people meet, so a caller can say
"install kubectl" in place of an exec error. No error text holds a password
(D10).

### The registry

```go
// Backend finds a database and starts a forward to it. A backend registers
// itself from init.
type Backend interface {
	// Lookup runs the lookup for u.
	Lookup(ctx context.Context, u *dburl.URL, cfg Config) (*Match, error)

	// Open starts a forward to the database that u names.
	Open(ctx context.Context, u *dburl.URL, cfg Config) (*Opened, error)
}

// Opened is what a backend returns from Open.
type Opened struct {
	// URL is the URL of the local end, as a string for dburl.Parse.
	URL string

	// Expires is when the credential in URL expires. It is zero when the
	// credential does not expire, as for Kubernetes.
	Expires time.Time

	// Renew makes a new URL and returns when it expires. It is nil when the
	// credential does not expire.
	Renew func(ctx context.Context) (string, time.Time, error)

	User     string
	Database string
	Target   Target
	Tunnel   Tunnel
}

// Tunnel is the running forward of a backend.
type Tunnel interface {
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Register makes a backend available for the scheme name, such as "k8s".
// It panics when the name is taken. Call it from init.
func Register(name string, b Backend)
```

`Resolve` applies the options, finds the backend, calls `Open` with the
`Config`, and builds the `Forward` around the `Opened`. A backend never builds
a `Forward`, so the rules for `URL`, `String`, `LogValue` and `Err` live in
one place.

`Opened` keeps the path and the credential apart. `Tunnel` is the path, and
`URL` with `Renew` is the credential. When the credential source becomes a
part of its own (`docs/PLAN.md`), it fills `URL` and `Renew`, and no caller
changes.

## Using it

A program imports the root package and the backend:

```go
import (
	"github.com/xo/dbfwd"
	_ "github.com/xo/dbfwd/k8s"
	"github.com/xo/dburl"
)

u, err := dburl.Parse("k+cnpg://pg.prod/app")
if err != nil {
	return err
}
if dbfwd.Handles(u) {
	fwd, err := dbfwd.Resolve(ctx, u)
	if err != nil {
		return err
	}
	defer fwd.Close()
	local, err := fwd.URL(ctx)
	if err != nil {
		return err
	}
	db, err := dburl.Open(local.String())
	// ...
}
```

The blank import follows `database/sql`: a program links only the backends it
imports. `usql` imports `k8s`, and later the others.

### List

`List` lists the databases that a backend can reach, for the `\k` command of
`usql` and `dbfwd list` (D18). `docs/LIST.md` holds what a row is, what it
reads and what RBAC it needs.

```go
// List lists the databases that u names, such as k://*, k://*.prod or
// k://pg*.prod. It reads no credential and starts no forward.
func List(ctx context.Context, u *dburl.URL, opts ...Option) (*Listing, error)

// ListURL builds the URL of a listing for the backend, such as "k8s", from
// a scope that a person typed and the fields that edit it.
func ListURL(backend, scope string, where ...Field) (*dburl.URL, error)

// Lister is a Backend that can list its databases.
type Lister interface {
	List(ctx context.Context, u *dburl.URL, cfg Config) (*Listing, error)
	ListURL(scope string, where []Field) (string, error)
}
```

`ListURL` lets `usql` and `dbfwd list` read a scope that a person typed in the
same way, such as `cnpg://*` with the field `namespace` set to `prod`. The
backend owns the rules, so the root package names no Kubernetes term.

`Listing` holds the scope that `List` resolved, one `Entry` for each
database, and a `Skip` for each kind that RBAC hid. An `Entry` holds the
`Target`, the operator, the shortest URL that `Resolve` maps to the same
database, the roles, the ready pods, the databases and where the password
comes from. No field holds a password. A backend that is not a `Lister`
returns `ErrUnsupported`.

## The k8s package

`k8s` registers itself as `k8s` from `init`, and exports nothing else. It
holds:

1. The parse of the host into a name and a namespace, and of its query keys,
   `context` and `role` (D6).
2. The lookup chain (D7).
3. The rules of each operator, and the map from an operator to a scheme (D8,
   D9).
4. The pod spec rules for a database with no operator.
5. The listing of the databases of a namespace or of every namespace (D18).
6. The `kubectl` runner.

The runner is an unexported interface, so that a test replaces it with a fake
or with recorded JSON (D12):

```go
type runner interface {
	// get runs kubectl get with args and returns its JSON.
	get(ctx context.Context, args ...string) ([]byte, error)

	// forward starts kubectl port-forward and returns once it prints the
	// local port.
	forward(ctx context.Context, args ...string) (*process, error)
}
```

The local port is chosen by `kubectl`: `dbfwd` asks for `:<remote port>` and
reads the port from the line `Forwarding from 127.0.0.1:<port>`.

A later backend has the same shape: a package that registers itself, an
unexported runner for its tool, and its own query keys and lookup.

## What usql said

`usql` reviewed this API. It fits: `WithInteractive` takes `l.Release` from
`rline` v0.8.0 as it is, `Handles` comes before `drivers.Open`, and `Target`
holds what `\conninfo` shows. `usql` opens its pool with a
`driver.Connector` of its own that calls `URL` for each new connection, and
it needs no fixed local port.
