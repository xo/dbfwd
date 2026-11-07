package k8s

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/xo/dbfwd"
)

// tool is the name of the tool that the backend runs.
const tool = "kubectl"

// Internal error values. A lookup step turns them into a step result, and
// neither leaves the package.
const (
	errNotFound     dbfwd.Error = "object not found"
	errNotInstalled dbfwd.Error = "kind not installed"
)

// runner runs kubectl. A test replaces it with a fake.
type runner interface {
	// get runs kubectl with args and returns its stdout.
	get(ctx context.Context, args ...string) ([]byte, error)

	// forward starts kubectl port-forward with args and returns once it
	// prints the local port.
	forward(ctx context.Context, args ...string) (tunnel, error)

	// watch starts kubectl get --watch with args, and returns its events.
	watch(args ...string) (stream, error)
}

// event is one event of a watch: ADDED, MODIFIED or DELETED, and the object.
type event struct {
	Type   string `json:"type"`
	Object object `json:"object"`
}

// stream is a running watch.
type stream interface {
	// events returns the events, and is closed when the watch stops.
	events() <-chan event

	// stop stops the watch.
	stop()
}

// tunnel is a running port-forward.
type tunnel interface {
	dbfwd.Tunnel

	// port returns the local port.
	port() int
}

// execRunner runs the real kubectl.
type execRunner struct {
	path        string
	env         []string
	interactive func(run func(tty *os.File) error) error

	// once makes only the first call run with the terminal.
	once sync.Once
}

// newExecRunner builds the runner from the configuration of a call.
func newExecRunner(cfg dbfwd.Config) *execRunner {
	return &execRunner{
		path:        cfg.Tool(tool),
		env:         append(os.Environ(), cfg.Env...),
		interactive: cfg.Interactive,
	}
}

// command builds a kubectl command that ctx stops. It never runs a shell.
func (r *execRunner) command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.path, args...) //nolint:gosec // the path and the arguments come from the caller and the URL, and no shell reads them
	cmd.Env = r.env
	return cmd
}

// get runs kubectl with args and returns its stdout. The first call runs
// with the terminal when the caller gave one, so that a credential plugin can
// prompt once.
func (r *execRunner) get(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	run := func(tty *os.File) error {
		cmd := r.command(ctx, args)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if tty != nil {
			cmd.Stdin = tty
			cmd.Stderr = io.MultiWriter(&stderr, tty)
		}
		return cmd.Run()
	}
	var err error
	first := false
	// reading the kubeconfig reaches no cluster, so it does not use up the
	// one call that gets the terminal
	if !isConfig(args) {
		r.once.Do(func() { first = true })
	}
	if first && r.interactive != nil {
		err = r.interactive(run)
	} else {
		err = run(nil)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, classify(err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// isConfig reports whether args run kubectl config, which reads only the
// kubeconfig.
func isConfig(args []string) bool {
	return slices.Contains(args, "config")
}

// classify turns a failed run of kubectl into an error value. It never
// returns the output of kubectl, which can hold more than the caller asked
// for.
func classify(err error, stderr string) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("running %s: %w", tool, dbfwd.ErrNoTool)
	}
	return classifyExit(exitErr.ExitCode(), stderr)
}

// classifyExit turns the exit status and the stderr of kubectl into an error
// value.
func classifyExit(code int, stderr string) error {
	switch s := strings.ToLower(stderr); {
	case strings.Contains(s, "doesn't have a resource type"):
		return errNotInstalled
	case strings.Contains(s, "notfound"), strings.Contains(s, "not found"):
		return errNotFound
	case strings.Contains(s, "forbidden"):
		return dbfwd.ErrForbidden
	case strings.Contains(s, "unauthorized"),
		strings.Contains(s, "must be logged in"),
		strings.Contains(s, "provide credentials"),
		strings.Contains(s, "getting credentials"):
		return dbfwd.ErrUnauthorized
	case strings.Contains(s, "context") && strings.Contains(s, "does not exist"):
		return fmt.Errorf("choosing the kube context: %w", dbfwd.ErrNotFound)
	}
	return fmt.Errorf("running %s, which exited with status %d", tool, code)
}

// forwardLine is the line that kubectl port-forward prints when it is ready.
var forwardLine = regexp.MustCompile(`^Forwarding from 127\.0\.0\.1:(\d+) `)

// forward starts kubectl port-forward and returns once it is ready. It never
// gets a terminal. Canceling ctx before it is ready stops it, and after that
// only Close does.
func (r *execRunner) forward(ctx context.Context, args ...string) (tunnel, error) {
	// The forward outlives ctx, which stops only its start, so it is not a
	// CommandContext. Close stops it.
	cmd := exec.Command(r.path, args...) //nolint:gosec,noctx // see above, and the path and the arguments reach no shell
	cmd.Env = r.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("starting %s port-forward: %w", tool, err)
	}
	t := &process{cmd: cmd, done: make(chan struct{})}
	cmd.Stderr = &t.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("running %s: %w", tool, dbfwd.ErrNoTool)
	}
	ready := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sent := false
		for sc.Scan() {
			if m := forwardLine.FindStringSubmatch(sc.Text()); m != nil && !sent {
				n, _ := strconv.Atoi(m[1])
				ready <- n
				sent = true
			}
		}
	}()
	go func() {
		t.err = cmd.Wait()
		close(t.done)
	}()
	select {
	case n := <-ready:
		t.local = n
		return t, nil
	case <-t.done:
		// the output of a port-forward holds no credential, so its last line
		// can say why it stopped
		stderr := t.stderr.String()
		return nil, fmt.Errorf("starting %s port-forward: %w: %s", tool, classify(t.err, stderr), lastLine(stderr))
	case <-ctx.Done():
		_ = t.Close()
		return nil, ctx.Err()
	}
}

// process is a running kubectl port-forward.
type process struct {
	cmd    *exec.Cmd
	local  int
	stderr lockedBuffer
	done   chan struct{}
	err    error
	once   sync.Once
}

// Done returns a channel that is closed when kubectl exits.
func (t *process) Done() <-chan struct{} { return t.done }

// Err returns why kubectl exited.
func (t *process) Err() error {
	select {
	case <-t.done:
	default:
		return nil
	}
	if line := lastLine(t.stderr.String()); line != "" {
		return fmt.Errorf("%s port-forward stopped: %s", tool, line)
	}
	if t.err != nil {
		return fmt.Errorf("%s port-forward stopped: %w", tool, t.err)
	}
	return fmt.Errorf("%s port-forward stopped", tool)
}

// Close stops kubectl and waits for it to exit.
func (t *process) Close() error {
	t.once.Do(func() {
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
	})
	<-t.done
	return nil
}

// port returns the local port.
func (t *process) port() int { return t.local }

// lastLine returns the last line of s that is not empty.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// lockedBuffer is a buffer that two goroutines can use.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// kube reads objects of one namespace and context.
type kube struct {
	r         runner
	context   string
	namespace string
}

// args returns the arguments that pick the context and the namespace.
func (k *kube) args(rest ...string) []string {
	var args []string
	if k.context != "" {
		args = append(args, "--context", k.context)
	}
	if k.namespace != "" {
		args = append(args, "--namespace", k.namespace)
	}
	return append(args, rest...)
}

// get reads one object.
func (k *kube) get(ctx context.Context, resource, name string) (object, error) {
	out, err := k.r.get(ctx, k.args("get", resource, name, "--output", "json")...)
	if err != nil {
		return nil, err
	}
	var o object
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, fmt.Errorf("reading %s %s: %w", resource, name, err)
	}
	return o, nil
}

// list reads the objects of a kind that match a label selector.
func (k *kube) list(ctx context.Context, resource, selector string) ([]object, error) {
	out, err := k.r.get(ctx, k.args("get", resource, "--selector", selector, "--output", "json")...)
	if err != nil {
		return nil, err
	}
	var o object
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, fmt.Errorf("reading %s: %w", resource, err)
	}
	return o.list("items"), nil
}

// resources returns the resources that the cluster has, by the names that
// kubectl get takes, such as clusters.postgresql.cnpg.io.
func (k *kube) resources(ctx context.Context) (map[string]bool, error) {
	out, err := k.r.get(ctx, k.args("api-resources", "--output", "name")...)
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool)
	for line := range strings.Lines(string(out)) {
		if s := strings.TrimSpace(line); s != "" {
			m[s] = true
		}
	}
	return m, nil
}

// secret reads the data of a Secret.
func (k *kube) secret(ctx context.Context, name string) (map[string]string, error) {
	o, err := k.get(ctx, "secret", name)
	if err != nil {
		return nil, fmt.Errorf("reading the secret %s: %w", name, err)
	}
	return secretData(o), nil
}

// current returns the current context and its namespace, from the
// kubeconfig.
func current(ctx context.Context, r runner, context string) (string, string, error) {
	args := []string{"config", "view", "--minify", "--output", "json"}
	if context != "" {
		args = append([]string{"--context", context}, args...)
	}
	out, err := r.get(ctx, args...)
	if err != nil {
		return "", "", fmt.Errorf("reading the kubeconfig: %w", err)
	}
	var o object
	if err := json.Unmarshal(out, &o); err != nil {
		return "", "", fmt.Errorf("reading the kubeconfig: %w", err)
	}
	name := o.str("current-context")
	namespace := ""
	for _, c := range o.list("contexts") {
		if c.str("name") == name {
			namespace = c.str("context", "namespace")
		}
	}
	if namespace == "" {
		namespace = "default"
	}
	return name, namespace, nil
}

// watch starts kubectl get --watch and decodes each event it prints. It never
// gets a terminal, and only stop or the end of kubectl stops it.
func (r *execRunner) watch(args ...string) (stream, error) {
	cmd := exec.Command(r.path, args...) //nolint:gosec,noctx // a watch outlives the call that starts it, and its arguments reach no shell
	cmd.Env = r.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("starting %s get --watch: %w", tool, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("running %s: %w", tool, dbfwd.ErrNoTool)
	}
	w := &watchProcess{cmd: cmd, ch: make(chan event)}
	go func() {
		defer close(w.ch)
		dec := json.NewDecoder(stdout)
		for {
			var e event
			if err := dec.Decode(&e); err != nil {
				_ = cmd.Wait()
				return
			}
			w.ch <- e
		}
	}()
	return w, nil
}

// watchProcess is a running kubectl get --watch.
type watchProcess struct {
	cmd  *exec.Cmd
	ch   chan event
	once sync.Once
}

func (w *watchProcess) events() <-chan event { return w.ch }

func (w *watchProcess) stop() {
	w.once.Do(func() {
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
	})
	// drain, so that the decoder can end
	for range w.ch {
	}
}
