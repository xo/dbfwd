package dbfwd_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// secret is the password of every fake forward. No printed form of a Forward
// may hold it.
const secret = "s3cr3t-pass"

// tunnels holds the tunnel that the fake backend opened for each host, so
// that a test can lose it.
var tunnels sync.Map

// captured holds the Config that the capture backend last saw.
var captured atomic.Pointer[dbfwd.Config]

func init() {
	dbfwd.Register("fake", fakeBackend{})
	dbfwd.Register("capture", capture{})
}

// fakeBackend opens a fakeTunnel. The host of the URL picks how it behaves.
type fakeBackend struct{}

func (fakeBackend) Lookup(_ context.Context, u *dburl.URL, _ dbfwd.Config) (*dbfwd.Match, error) {
	if u.Host == "missing" {
		return nil, &dbfwd.LookupError{
			Name:  "missing",
			Scope: []dbfwd.Field{{Name: "namespace", Value: "prod"}},
			Steps: []dbfwd.Step{{Kind: "Service", Result: "no match"}},
			Err:   dbfwd.ErrNotFound,
		}
	}
	return &dbfwd.Match{Target: target()}, nil
}

func (fakeBackend) Open(_ context.Context, u *dburl.URL, _ dbfwd.Config) (*dbfwd.Opened, error) {
	o := &dbfwd.Opened{
		URL:      "postgres://app:" + secret + "@127.0.0.1:41234/app",
		User:     "app",
		Database: "app",
		Target:   target(),
		Tunnel:   newFakeTunnel(),
	}
	tunnels.Store(u.Host, o.Tunnel)
	if u.Host == "expiring" {
		var n atomic.Int32
		o.Expires = time.Now()
		o.Renew = func(context.Context) (string, time.Time, error) {
			i := n.Add(1)
			return fmt.Sprintf("postgres://app:%s%d@127.0.0.1:41234/app", secret, i), time.Now().Add(time.Hour), nil
		}
	}
	return o, nil
}

func target() dbfwd.Target {
	return dbfwd.Target{
		Backend: "fake",
		Scope:   []dbfwd.Field{{Name: "context", Value: "kind-prod"}, {Name: "namespace", Value: "prod"}},
		Kind:    "Service",
		Name:    "pg-rw",
		Owner:   "CloudNativePG Cluster pg",
		Type:    "postgres",
	}
}

// fakeTunnel is a tunnel that a test can lose.
type fakeTunnel struct {
	done chan struct{}
	once sync.Once
	err  atomic.Value
}

func newFakeTunnel() *fakeTunnel {
	return &fakeTunnel{done: make(chan struct{})}
}

func (t *fakeTunnel) Done() <-chan struct{} { return t.done }

func (t *fakeTunnel) Err() error {
	if err, ok := t.err.Load().(error); ok {
		return err
	}
	return nil
}

func (t *fakeTunnel) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}

func (t *fakeTunnel) lose(err error) {
	t.err.Store(err)
	t.once.Do(func() { close(t.done) })
}

func fakeURL(host string) *dburl.URL {
	u := &dburl.URL{SchemeName: "fake"}
	u.Host = host
	return u
}

func TestHandles(t *testing.T) {
	t.Parallel()
	if !dbfwd.Handles(fakeURL("pg")) {
		t.Errorf("Handles(fake) = false, expected true")
	}
	if dbfwd.Handles(&dburl.URL{SchemeName: "postgres"}) {
		t.Errorf("Handles(postgres) = true, expected false")
	}
	if dbfwd.Handles(nil) {
		t.Errorf("Handles(nil) = true, expected false")
	}
}

func TestResolveWithNoBackend(t *testing.T) {
	t.Parallel()
	_, err := dbfwd.Resolve(t.Context(), &dburl.URL{SchemeName: "nope"})
	if !errors.Is(err, dbfwd.ErrNoBackend) {
		t.Fatalf("Resolve(nope) error = %v, expected %v", err, dbfwd.ErrNoBackend)
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("Register(fake) twice did not panic")
		}
	}()
	dbfwd.Register("fake", fakeBackend{})
}

func TestForward(t *testing.T) {
	t.Parallel()
	fwd, err := dbfwd.Resolve(t.Context(), fakeURL("pg"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	u, err := fwd.URL(t.Context())
	if err != nil {
		t.Fatalf("URL error = %v", err)
	}
	if u.Port() != "41234" || u.User.Username() != "app" {
		t.Errorf("URL = host %q user %q, expected port 41234 and user app", u.Host, u.User.Username())
	}
	if fwd.User != "app" || fwd.Database != "app" {
		t.Errorf("User, Database = %q, %q, expected app, app", fwd.User, fwd.Database)
	}
	if err := fwd.Err(); err != nil {
		t.Errorf("Err while running = %v, expected nil", err)
	}
	if err := fwd.Close(); err != nil {
		t.Errorf("Close error = %v", err)
	}
	if err := fwd.Close(); err != nil {
		t.Errorf("second Close error = %v", err)
	}
	select {
	case <-fwd.Done():
	default:
		t.Errorf("Done is open after Close")
	}
	if err := fwd.Err(); err != nil {
		t.Errorf("Err after Close = %v, expected nil", err)
	}
	if _, err := fwd.URL(t.Context()); !errors.Is(err, dbfwd.ErrClosed) {
		t.Errorf("URL after Close error = %v, expected %v", err, dbfwd.ErrClosed)
	}
}

func TestForwardLost(t *testing.T) {
	t.Parallel()
	fwd, err := dbfwd.Resolve(t.Context(), fakeURL("lost"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	defer fwd.Close()
	lost := errors.New("pod deleted")
	tun, _ := tunnels.Load("lost")
	ft, ok := tun.(*fakeTunnel)
	if !ok {
		t.Fatalf("tunnel of lost = %T, expected *fakeTunnel", tun)
	}
	ft.lose(lost)
	<-fwd.Done()
	err = fwd.Err()
	if !errors.Is(err, dbfwd.ErrForwardLost) || !errors.Is(err, lost) {
		t.Errorf("Err after loss = %v, expected %v and %v", err, dbfwd.ErrForwardLost, lost)
	}
	if _, err := fwd.URL(t.Context()); !errors.Is(err, dbfwd.ErrForwardLost) {
		t.Errorf("URL after loss error = %v, expected %v", err, dbfwd.ErrForwardLost)
	}
}

func TestForwardRenewsAnExpiredCredential(t *testing.T) {
	t.Parallel()
	fwd, err := dbfwd.Resolve(t.Context(), fakeURL("expiring"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	defer fwd.Close()
	var wg sync.WaitGroup
	passwords := make([]string, 8)
	for i := range passwords {
		wg.Go(func() {
			u, err := fwd.URL(t.Context())
			if err != nil {
				t.Errorf("URL error = %v", err)
				return
			}
			passwords[i], _ = u.User.Password()
		})
	}
	wg.Wait()
	for _, p := range passwords {
		if p != secret+"1" {
			t.Errorf("password = %q, expected one renewal to %q", p, secret+"1")
		}
	}
}

func TestPrintingAForwardHidesThePassword(t *testing.T) {
	t.Parallel()
	fwd, err := dbfwd.Resolve(t.Context(), fakeURL("pg"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	defer fwd.Close()
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("connected", "forward", fwd)
	for _, s := range []string{
		fmt.Sprint(fwd), fmt.Sprintf("%v %s %+v %#v", fwd, fwd, fwd, fwd), logged.String(),
	} {
		if strings.Contains(s, secret) {
			t.Errorf("printed form holds the password: %s", s)
		}
	}
	const want = "Service pg-rw in context kind-prod, namespace prod, owned by CloudNativePG Cluster pg"
	if got := fwd.String(); got != want {
		t.Errorf("String = %q, expected %q", got, want)
	}
}

func TestLookupError(t *testing.T) {
	t.Parallel()
	_, err := dbfwd.Lookup(t.Context(), fakeURL("missing"))
	if !errors.Is(err, dbfwd.ErrNotFound) {
		t.Fatalf("Lookup error = %v, expected %v", err, dbfwd.ErrNotFound)
	}
	var le *dbfwd.LookupError
	if !errors.As(err, &le) || len(le.Steps) != 1 {
		t.Fatalf("Lookup error = %#v, expected a LookupError with one step", err)
	}
	const want = "finding missing in namespace prod (tried Service: no match): not found"
	if got := err.Error(); got != want {
		t.Errorf("Error = %q, expected %q", got, want)
	}
}

func TestOptions(t *testing.T) {
	t.Parallel()
	_, _ = dbfwd.Lookup(t.Context(), &dburl.URL{SchemeName: "capture"},
		dbfwd.WithTool("kubectl", "/opt/kubectl"),
		dbfwd.WithEnv("KUBECONFIG", "/a"),
		dbfwd.WithEnv("KUBECONFIG", "/b"),
	)
	got := captured.Load()
	if got.Tool("kubectl") != "/opt/kubectl" || got.Tool("aws") != "aws" {
		t.Errorf("Tool = %q, %q, expected /opt/kubectl, aws", got.Tool("kubectl"), got.Tool("aws"))
	}
	if strings.Join(got.Env, " ") != "KUBECONFIG=/a KUBECONFIG=/b" {
		t.Errorf("Env = %v, expected both values in order", got.Env)
	}
}

type capture struct{}

func (capture) Lookup(_ context.Context, _ *dburl.URL, cfg dbfwd.Config) (*dbfwd.Match, error) {
	captured.Store(&cfg)
	return &dbfwd.Match{}, nil
}

func (capture) Open(context.Context, *dburl.URL, dbfwd.Config) (*dbfwd.Opened, error) {
	return nil, dbfwd.ErrUnsupported
}

func TestListUnsupported(t *testing.T) {
	t.Parallel()
	// the fake backend is no Lister
	if _, err := dbfwd.List(t.Context(), fakeURL("*")); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("List error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
	if _, err := dbfwd.List(t.Context(), &dburl.URL{SchemeName: "nope"}); !errors.Is(err, dbfwd.ErrNoBackend) {
		t.Errorf("List error = %v, expected %v", err, dbfwd.ErrNoBackend)
	}
	if _, err := dbfwd.ListURL("fake", "*"); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("ListURL error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
	if _, err := dbfwd.ListURL("nope", "*"); !errors.Is(err, dbfwd.ErrNoBackend) {
		t.Errorf("ListURL error = %v, expected %v", err, dbfwd.ErrNoBackend)
	}
}
