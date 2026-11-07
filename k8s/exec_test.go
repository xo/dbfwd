package k8s

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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
	case "watch":
		fmt.Println(`{"type":"ADDED","object":{"metadata":{"name":"pg-1"},"status":{"phase":"Running"}}}`)
		fmt.Println(`{"type":"DELETED","object":{"metadata":{"name":"pg-1"}}}`)
		time.Sleep(time.Minute)
		return 0
	case "parent":
		// a dbfwd that starts a port-forward, says its pid, and waits to be
		// killed
		r := newExecRunner(dbfwd.Config{Tools: map[string]string{tool: os.Args[0]}, Env: []string{helperEnv + "=forward"}})
		t, err := r.forward(context.Background(), "port-forward")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		p, _ := t.(*process)
		fmt.Println(p.cmd.Process.Pid)
		time.Sleep(time.Minute)
		return 0
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

func TestExecRunnerWatch(t *testing.T) {
	t.Parallel()
	w, err := helperRunner(t, "watch").watch("get", "pod", "pg-1", "--watch")
	if err != nil {
		t.Fatalf("watch error = %v", err)
	}
	var types []string
	for e := range w.events() {
		types = append(types, e.Type+" "+e.Object.name())
		if len(types) == 2 {
			break
		}
	}
	w.stop()
	if got := strings.Join(types, ", "); got != "ADDED pg-1, DELETED pg-1" {
		t.Errorf("events = %s, expected ADDED pg-1, DELETED pg-1", got)
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

func TestChildDiesWithParent(t *testing.T) {
	t.Parallel()
	if _, ok := deathSignal(&syscall.SysProcAttr{}); !ok {
		t.Skip("the system has no Pdeathsig, so a child outlives a killed parent (D17)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable error = %v", err)
	}
	parent := exec.CommandContext(t.Context(), exe)
	parent.Env = append(os.Environ(), helperEnv+"=parent")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(out)
	if !sc.Scan() {
		t.Fatalf("the parent printed no pid: %v", parent.Wait())
	}
	pid, err := strconv.Atoi(sc.Text())
	if err != nil {
		t.Fatalf("pid = %q: %v", sc.Text(), err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	// SIGKILL, so that no Close runs
	_ = parent.Process.Kill()
	_ = parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for child.Signal(syscall.Signal(0)) == nil {
		if time.Now().After(deadline) {
			_ = child.Kill()
			t.Fatalf("kubectl %d still runs after its parent was killed", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
