package test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
	_ "github.com/xo/dbfwd/k8s"
)

// update records the kubectl output of each case to ../testdata, for the
// replay tests of the root module.
var update = flag.Bool("update", false, "record the replay files to ../testdata")

// The environment that picks the cluster.
const (
	// envKubeconfig and envContext name the cluster. dbrun dsn --json prints
	// both for a kind cluster.
	envKubeconfig = "DBFWD_TEST_KUBECONFIG"
	envContext    = "DBFWD_TEST_CONTEXT"

	// envNamespace names a namespace of a cluster that already runs. The
	// tests then install nothing, and work only in that namespace, which must
	// carry the label testLabel.
	envNamespace = "DBFWD_TEST_NAMESPACE"
)

// testLabel marks a namespace of an existing cluster that the tests can use.
const testLabel = "dbfwd.xo/test=true"

// c is the cluster of the run, or nil when no cluster was named.
var c *cluster

func TestMain(m *testing.M) {
	flag.Parse()
	kubeconfig, kubeContext := os.Getenv(envKubeconfig), os.Getenv(envContext)
	if kubeconfig == "" || kubeContext == "" {
		fmt.Fprintf(os.Stderr, "the cluster tests need %s and %s, which dbrun dsn --json prints; skipping\n", envKubeconfig, envContext)
		os.Exit(m.Run())
	}
	var err error
	if c, err = newCluster(kubeconfig, kubeContext, os.Getenv(envNamespace)); err != nil {
		fmt.Fprintln(os.Stderr, "setting up the cluster:", err)
		os.Exit(1)
	}
	code := m.Run()
	c.cleanup()
	os.Exit(code)
}

// cluster is the cluster that the tests run against.
type cluster struct {
	kubeconfig string
	context    string

	// existing names the one namespace of an existing cluster, or is empty
	// for a kind cluster that the tests own.
	existing string

	// recorder is the path of the recording kubectl, when update is set.
	recorder string
	tmp      string

	mu       sync.Mutex
	installs map[string]*install
}

// install is one operator or chart, installed once per run.
type install struct {
	once sync.Once
	err  error
}

func newCluster(kubeconfig, kubeContext, existing string) (*cluster, error) {
	tmp, err := os.MkdirTemp("", "dbfwd-test-")
	if err != nil {
		return nil, err
	}
	cl := &cluster{kubeconfig: kubeconfig, context: kubeContext, existing: existing, tmp: tmp, installs: map[string]*install{}}
	if existing != "" {
		out, err := cl.run("get", "namespace", existing, "--selector", testLabel, "--output", "name")
		if err != nil || strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("using the namespace %s, which must exist and carry the label %s", existing, testLabel)
		}
	}
	if *update {
		cl.recorder = filepath.Join(tmp, "kubectl-record")
		build := exec.CommandContext(context.Background(), "go", "build", "-o", cl.recorder, "./cmd/kubectl-record")
		if out, err := build.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building the recording kubectl: %w: %s", err, out)
		}
	}
	return cl, nil
}

func (cl *cluster) cleanup() {
	_ = os.RemoveAll(cl.tmp)
}

// skip skips a test when no cluster was named.
func skip(t *testing.T) {
	t.Helper()
	if c == nil {
		t.Skipf("the cluster tests need %s and %s", envKubeconfig, envContext)
	}
}

// env returns the environment of kubectl and helm.
func (cl *cluster) env() []string {
	return append(os.Environ(), "KUBECONFIG="+cl.kubeconfig)
}

// run runs kubectl and returns its stdout.
func (cl *cluster) run(args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "kubectl", append([]string{"--context", cl.context}, args...)...)
	cmd.Env = cl.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// kubectl runs kubectl and fails the test when it fails.
func (cl *cluster) kubectl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := cl.run(args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// apply applies a manifest in a namespace, trying again while a webhook of an
// operator starts.
func (cl *cluster) apply(t *testing.T, namespace, manifest string) {
	t.Helper()
	cl.eventually(t, 3*time.Minute, func() error {
		cmd := exec.CommandContext(context.Background(), "kubectl", "--context", cl.context, "--namespace", namespace, "apply", "--server-side", "--force-conflicts", "--filename", "-")
		cmd.Env = cl.env()
		cmd.Stdin = strings.NewReader(manifest)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("applying a manifest: %w: %s", err, out)
		}
		return nil
	})
}

// helm runs helm against the cluster.
func (cl *cluster) helm(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "helm", append([]string{"--kube-context", cl.context}, args...)...)
	cmd.Env = cl.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helm %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// eventually calls fn until it returns nil or the time runs out.
func (cl *cluster) eventually(t *testing.T, d time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		err := fn()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting %s: %v", d, err)
		}
		time.Sleep(3 * time.Second)
	}
}

// once runs fn once per run for the key, such as an operator to install. A
// later caller gets the error of the first.
func (cl *cluster) once(t *testing.T, key string, fn func(t *testing.T) error) {
	t.Helper()
	cl.mu.Lock()
	in, ok := cl.installs[key]
	if !ok {
		in = &install{}
		cl.installs[key] = in
	}
	cl.mu.Unlock()
	in.once.Do(func() { in.err = fn(t) })
	if in.err != nil {
		t.Fatalf("installing %s: %v", key, in.err)
	}
}

// namespace returns the namespace of a suite, which it creates. On an
// existing cluster it is the one namespace that was named.
func (cl *cluster) namespace(t *testing.T, suite string) string {
	t.Helper()
	if cl.existing != "" {
		return cl.existing
	}
	ns := "dbfwd-" + suite
	cl.waitGone(t, ns)
	if _, err := cl.run("get", "namespace", ns); err != nil {
		cl.kubectl(t, "create", "namespace", ns)
	}
	return ns
}

// waitGone waits until a namespace that is being deleted is gone, so that a
// suite that runs again can make it afresh.
func (cl *cluster) waitGone(t *testing.T, ns string) {
	t.Helper()
	cl.eventually(t, defaultWait, func() error {
		out, err := cl.run("get", "namespace", ns, "--output", "jsonpath={.status.phase}")
		if err == nil && out == "Terminating" {
			return errNotReady
		}
		return nil
	})
}

// operator installs an operator once. On an existing cluster it installs
// nothing, and skips the test when the CRD of the operator is missing.
func (cl *cluster) operator(t *testing.T, name, crd string, fn func(t *testing.T) error) {
	t.Helper()
	if cl.existing != "" {
		if _, err := cl.run("get", "crd", crd); err != nil {
			t.Skipf("the cluster has no %s", name)
		}
		return
	}
	cl.once(t, name, fn)
}

// options returns the options of dbfwd for the cluster. With update, kubectl
// is the recording kubectl, which writes to dir.
func (cl *cluster) options(dir string) []dbfwd.Option {
	opts := []dbfwd.Option{dbfwd.WithEnv("KUBECONFIG", cl.kubeconfig)}
	if cl.recorder != "" && dir != "" {
		opts = append(opts,
			dbfwd.WithTool("kubectl", cl.recorder),
			dbfwd.WithEnv("DBFWD_RECORD_DIR", dir),
			dbfwd.WithEnv("DBFWD_RECORD_CONTEXT", cl.context),
		)
	}
	return opts
}

// withContext adds the context of the cluster to a URL.
func (cl *cluster) withContext(raw string) string {
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "context=" + cl.context
}

// check is one URL that a fixture answers to, and what it must reach.
type check struct {
	// url is the URL with no context, such as k://pg-rw.NS/.
	url string

	// user and database are what the connection must report.
	user     string
	database string

	// kind is the kind of the target that must match, such as Service.
	kind string

	// name names the recorded case when the URL holds a name that changes
	// on every run, such as the name of a pod of a Deployment.
	name string
}

// folder returns the name of the recorded case.
func (ch check) folder() string {
	if ch.name != "" {
		return ch.name
	}
	return caseName(ch.url)
}

// connect resolves a URL, opens the database through the forward with the
// real driver, and checks who it is and where.
func (cl *cluster) connect(t *testing.T, suite string, ch check) *dbfwd.Forward {
	t.Helper()
	raw := cl.withContext(ch.url)
	u, err := dburl.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", ch.url, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	dir := ""
	if *update && suite != "" {
		dir = filepath.Join("..", "testdata", suite, ch.folder())
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fwd, err := dbfwd.Resolve(ctx, u, cl.options(dir)...)
	if err != nil {
		t.Fatalf("resolving %s: %v", ch.url, err)
	}
	t.Cleanup(func() { _ = fwd.Close() })
	if ch.kind != "" && fwd.Target.Kind != ch.kind {
		t.Errorf("%s matched %s, expected a %s", ch.url, fwd.Target, ch.kind)
	}
	user, database := query(ctx, t, fwd)
	if (ch.user != anyValue && user != ch.user) || (ch.database != anyValue && database != ch.database) {
		t.Errorf("%s connected as %s to %q, expected %s to %q", ch.url, user, database, ch.user, ch.database)
	}
	if ch.user != anyValue && fwd.User != ch.user {
		t.Errorf("%s: Forward.User = %q, expected %q", ch.url, fwd.User, ch.user)
	}
	if *update {
		writeExpect(t, dir, raw, fwd)
	}
	return fwd
}

// query opens the database of a forward and returns the current user and
// database.
func query(ctx context.Context, t *testing.T, fwd *dbfwd.Forward) (string, string) {
	t.Helper()
	return verify(ctx, t, fwd)
}

// expectErr resolves a URL and checks that it fails with want.
func (cl *cluster) expectErr(t *testing.T, raw string, want error) {
	t.Helper()
	u, err := dburl.Parse(cl.withContext(raw))
	if err != nil {
		t.Fatalf("parsing %s: %v", raw, err)
	}
	fwd, err := dbfwd.Resolve(t.Context(), u, cl.options("")...)
	if err == nil {
		_ = fwd.Close()
		t.Fatalf("resolving %s worked, expected %v", raw, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("resolving %s: %v, expected %v", raw, err, want)
	}
}

// checkClose checks that Close frees the local port.
func checkClose(t *testing.T, fwd *dbfwd.Forward) {
	t.Helper()
	local, err := fwd.URL(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := fwd.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	select {
	case <-fwd.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("Done did not close after Close")
	}
	if err := fwd.Err(); err != nil {
		t.Errorf("Err after Close = %v, expected nil", err)
	}
	d := net.Dialer{Timeout: time.Second}
	conn, err := d.DialContext(t.Context(), "tcp", local.Host)
	if err == nil {
		_ = conn.Close()
		t.Errorf("the local port %s still answers after Close", local.Host)
	}
}

// caseName turns a URL into the name of a folder.
func caseName(raw string) string {
	r := strings.NewReplacer("://", "_", "/", "_", ":", "_", "@", "_at_", "?", "_", "&", "_", "=", "_", "+", "_")
	return strings.Trim(r.Replace(raw), "_")
}

// errNotReady is the error of a wait that has not ended.
var errNotReady = errors.New("not ready yet")

// defaultWait is how long a fixture can take to become ready.
const defaultWait = 10 * time.Minute

// anyValue in a check accepts any user or database, for a server that
// reports a name dbfwd cannot know, such as a generated superuser.
const anyValue = "*"
