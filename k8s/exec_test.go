package k8s

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/xo/dbfwd"
)

// helperEnv makes the test binary act as kubectl. Its value picks what it
// does.
const helperEnv = "DBFWD_FAKE_KUBECTL"

// TestMain runs the test binary as kubectl when helperEnv is set.
func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(fakeKubectl(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeKubectl acts as kubectl for one mode.
func fakeKubectl(mode string, args []string) int {
	switch mode {
	case "get":
		fmt.Println(`{"metadata":{"name":"pg"},"args":"` + strings.Join(args, " ") + `"}`)
		return 0
	case "notfound":
		fmt.Fprintln(os.Stderr, `Error from server (NotFound): services "pg" not found`)
		return 1
	case "forward":
		fmt.Println("Forwarding from 127.0.0.1:43210 -> 5432")
		fmt.Println("Forwarding from [::1]:43210 -> 5432")
		time.Sleep(time.Minute)
		return 0
	case "forward-then-die":
		fmt.Println("Forwarding from 127.0.0.1:43211 -> 5432")
		time.Sleep(100 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "error: lost connection to pod")
		return 1
	case "forward-fails":
		fmt.Fprintln(os.Stderr, `Error from server (Forbidden): pods "pg-1" is forbidden`)
		return 1
	}
	return 2
}

// helperRunner is an execRunner that runs the test binary as kubectl.
func helperRunner(t *testing.T, mode string) *execRunner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable error = %v", err)
	}
	return newExecRunner(dbfwd.Config{
		Tools: map[string]string{tool: exe},
		Env:   []string{helperEnv + "=" + mode},
	})
}

func TestExecRunnerGet(t *testing.T) {
	t.Parallel()
	k := &kube{r: helperRunner(t, "get"), context: "kind-test", namespace: "prod"}
	o, err := k.get(t.Context(), "service", "pg")
	if err != nil {
		t.Fatalf("get error = %v", err)
	}
	if want := "--context kind-test --namespace prod get service pg --output json"; o.str("args") != want {
		t.Errorf("args = %q, expected %q", o.str("args"), want)
	}
	k = &kube{r: helperRunner(t, "notfound"), namespace: "prod"}
	if _, err := k.get(t.Context(), "service", "pg"); !errors.Is(err, errNotFound) {
		t.Errorf("get error = %v, expected %v", err, errNotFound)
	}
}

func TestExecRunnerMissingTool(t *testing.T) {
	t.Parallel()
	r := newExecRunner(dbfwd.Config{Tools: map[string]string{tool: "/nonexistent/kubectl"}})
	if _, err := r.get(t.Context(), "version"); !errors.Is(err, dbfwd.ErrNoTool) {
		t.Errorf("get error = %v, expected %v", err, dbfwd.ErrNoTool)
	}
	if _, err := r.forward(t.Context(), "port-forward"); !errors.Is(err, dbfwd.ErrNoTool) {
		t.Errorf("forward error = %v, expected %v", err, dbfwd.ErrNoTool)
	}
}

func TestExecRunnerInteractiveOnce(t *testing.T) {
	t.Parallel()
	r := helperRunner(t, "get")
	calls := 0
	r.interactive = func(run func(tty *os.File) error) error {
		calls++
		return run(nil)
	}
	for _, args := range [][]string{
		{"config", "view"}, {"get", "service", "pg"}, {"get", "service", "pg"},
	} {
		if _, err := r.get(t.Context(), args...); err != nil {
			t.Fatalf("get %v error = %v", args, err)
		}
	}
	if calls != 1 {
		t.Errorf("interactive ran %d times, expected once, for the first get", calls)
	}
}

func TestExecRunnerForward(t *testing.T) {
	t.Parallel()
	tun, err := helperRunner(t, "forward").forward(t.Context(), "port-forward", "service/pg", ":5432")
	if err != nil {
		t.Fatalf("forward error = %v", err)
	}
	if tun.port() != 43210 {
		t.Errorf("port = %d, expected 43210", tun.port())
	}
	if err := tun.Close(); err != nil {
		t.Errorf("Close error = %v", err)
	}
	select {
	case <-tun.Done():
	default:
		t.Errorf("Done is open after Close")
	}
}

func TestExecRunnerForwardLost(t *testing.T) {
	t.Parallel()
	tun, err := helperRunner(t, "forward-then-die").forward(t.Context(), "port-forward")
	if err != nil {
		t.Fatalf("forward error = %v", err)
	}
	select {
	case <-tun.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("forward did not stop")
	}
	if err := tun.Err(); err == nil || !strings.Contains(err.Error(), "lost connection to pod") {
		t.Errorf("Err = %v, expected the last line of kubectl", err)
	}
}

func TestExecRunnerForwardFails(t *testing.T) {
	t.Parallel()
	_, err := helperRunner(t, "forward-fails").forward(t.Context(), "port-forward")
	if !errors.Is(err, dbfwd.ErrForbidden) {
		t.Errorf("forward error = %v, expected %v", err, dbfwd.ErrForbidden)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	exitErr := exec.CommandContext(t.Context(), "false").Run()
	tests := []struct {
		stderr string
		want   error
	}{
		{`error: the server doesn't have a resource type "clusters"`, errNotInstalled},
		{`Error from server (NotFound): pods "x" not found`, errNotFound},
		{`Error from server (Forbidden): secrets "x" is forbidden`, dbfwd.ErrForbidden},
		{`error: You must be logged in to the server (Unauthorized)`, dbfwd.ErrUnauthorized},
		{`error: getting credentials: exec: executable kubelogin failed`, dbfwd.ErrUnauthorized},
	}
	for _, test := range tests {
		if got := classify(exitErr, test.stderr); !errors.Is(got, test.want) {
			t.Errorf("classify(%q) = %v, expected %v", test.stderr, got, test.want)
		}
	}
	if got := classify(errors.New("exec: not found"), ""); !errors.Is(got, dbfwd.ErrNoTool) {
		t.Errorf("classify(not an exit) = %v, expected %v", got, dbfwd.ErrNoTool)
	}
}
