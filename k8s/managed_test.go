package k8s

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xo/dbfwd"
)

// cnpgPods adds two pods of the cluster pg, and makes name the primary.
func cnpgPods(r *fakeRunner, primary string) {
	for _, name := range []string{"pg-1", "pg-2"} {
		role := "replica"
		if name == primary {
			role = "primary"
		}
		r.add("pod", map[string]any{
			"metadata": meta(name, map[string]string{"cnpg.io/cluster": "pg", "cnpg.io/instanceRole": role}, cnpgGroup, "Cluster", "pg"),
		})
	}
}

// startPrimary starts a managed forward to the primary of the cluster pg.
func startPrimary(t *testing.T, r *fakeRunner, wait time.Duration) *managed {
	t.Helper()
	k := &kube{r: r, namespace: "prod"}
	p := &picker{k: k, forward: "service/pg-rw", pods: "cnpg.io/cluster=pg,cnpg.io/instanceRole=primary", port: 5432}
	m, err := startManaged(t.Context(), k, p, wait)
	if err != nil {
		t.Fatalf("startManaged error = %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// waitForwards waits until dbfwd started n forwards.
func waitForwards(t *testing.T, r *fakeRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.forwardCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("forwards = %d, expected %d: %v", r.forwardCount(), n, r.forwards)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// primaryService adds the Service pg-rw, whose selector picks the primary.
func primaryService(r *fakeRunner) {
	r.add("service", map[string]any{
		"metadata": meta("pg-rw", nil, cnpgGroup, "Cluster", "pg"),
		"spec": map[string]any{
			"selector": map[string]any{"cnpg.io/cluster": "pg", "cnpg.io/instanceRole": "primary"},
			"ports":    []any{map[string]any{"port": 5432}},
		},
	})
}

func TestManagedReconnectsWhenThePodIsDeleted(t *testing.T) {
	t.Parallel()
	r := newFake()
	primaryService(r)
	cnpgPods(r, "pg-1")
	m := startPrimary(t, r, time.Minute)
	if !strings.HasSuffix(r.forwards[0], "pod/pg-1 :5432") {
		t.Fatalf("first forward = %q, expected pod/pg-1", r.forwards[0])
	}
	// the cluster fails over to pg-2, and pg-1 goes
	r.mu.Lock()
	delete(r.objects, "pod/pg-1")
	r.mu.Unlock()
	cnpgPods(r, "pg-2")
	r.mu.Lock()
	delete(r.objects, "pod/pg-1")
	r.mu.Unlock()
	r.stream(t, "pg-1").ch <- event{Type: "DELETED", Object: object{"metadata": map[string]any{"name": "pg-1"}}}
	waitForwards(t, r, 2)
	if want := "pod/pg-2 41234:5432"; !strings.HasSuffix(r.forwards[1], want) {
		t.Errorf("second forward = %q, expected %s on the same local port", r.forwards[1], want)
	}
	select {
	case <-m.Done():
		t.Errorf("the forward stopped, expected it to reconnect")
	default:
	}
}

func TestManagedReconnectsWhenTheRoleMoves(t *testing.T) {
	t.Parallel()
	r := newFake()
	primaryService(r)
	cnpgPods(r, "pg-1")
	startPrimary(t, r, time.Minute)
	// a switchover makes pg-1 a replica and pg-2 the primary
	cnpgPods(r, "pg-2")
	r.mu.Lock()
	pod := r.objects["pod/pg-1"]
	r.mu.Unlock()
	m, _ := pod.(map[string]any)
	r.stream(t, "pg-1").ch <- event{Type: "MODIFIED", Object: object(m)}
	waitForwards(t, r, 2)
	if want := "pod/pg-2 41234:5432"; !strings.HasSuffix(r.forwards[1], want) {
		t.Errorf("second forward = %q, expected %s", r.forwards[1], want)
	}
}

func TestManagedGivesUp(t *testing.T) {
	t.Parallel()
	r := newFake()
	primaryService(r)
	cnpgPods(r, "pg-1")
	m := startPrimary(t, r, 300*time.Millisecond)
	r.mu.Lock()
	delete(r.objects, "pod/pg-1")
	delete(r.objects, "pod/pg-2")
	r.mu.Unlock()
	r.stream(t, "pg-1").ch <- event{Type: "DELETED", Object: object{}}
	select {
	case <-m.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the forward did not give up")
	}
	if err := m.Err(); !errors.Is(err, dbfwd.ErrForwardLost) {
		t.Errorf("Err = %v, expected %v", err, dbfwd.ErrForwardLost)
	}
}

func TestManagedClose(t *testing.T) {
	t.Parallel()
	r := newFake()
	primaryService(r)
	cnpgPods(r, "pg-1")
	m := startPrimary(t, r, time.Minute)
	if err := m.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-m.Done():
	default:
		t.Errorf("Done is open after Close")
	}
	if err := m.Err(); err != nil {
		t.Errorf("Err after Close = %v, expected nil", err)
	}
	if n := r.forwardCount(); n != 1 {
		t.Errorf("forwards = %d after Close, expected 1", n)
	}
}

func TestParseSelector(t *testing.T) {
	t.Parallel()
	sel, err := parseSelector("a=1,role in (master,primary),b=2")
	if err != nil {
		t.Fatal(err)
	}
	if !sel.matches(map[string]string{"a": "1", "role": "primary", "b": "2"}) {
		t.Errorf("selector does not match the primary")
	}
	if sel.matches(map[string]string{"a": "1", "role": "replica", "b": "2"}) {
		t.Errorf("selector matches a replica")
	}
}

// pod returns a pod with a phase and a Ready condition.
func pod(name, phase, isReady string) object {
	return object{
		"metadata": map[string]any{"name": name},
		"status": map[string]any{
			"phase":      phase,
			"conditions": []any{map[string]any{"type": "Ready", "status": isReady}},
		},
	}
}

func TestChoosePod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pods []object
		want string
	}{
		{"a ready pod comes first", []object{pod("a", "Running", "False"), pod("b", "Running", "True")}, "b"},
		{"a pod that runs when none is ready", []object{pod("b", "Pending", "False"), pod("a", "Running", "False")}, "a"},
		{"no pod runs", []object{pod("a", "Pending", "False"), pod("b", "Failed", "False")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := choosePod(tt.pods)
			switch {
			case tt.want == "" && ok:
				t.Errorf("choosePod = %s, expected none", got.name())
			case tt.want != "" && (!ok || got.name() != tt.want):
				t.Errorf("choosePod = %v, %t, expected %s", got.name(), ok, tt.want)
			}
		})
	}
}

func TestManagedKeepsAnUnreadyPod(t *testing.T) {
	t.Parallel()
	r := newFake()
	primaryService(r)
	cnpgPods(r, "pg-2")
	unready := map[string]any{
		"phase":      "Running",
		"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
	}
	r.mu.Lock()
	o, _ := r.objects["pod/pg-2"].(map[string]any)
	o["status"] = unready
	r.mu.Unlock()
	m := startPrimary(t, r, time.Minute)
	if !strings.HasSuffix(r.forwards[0], "pod/pg-2 :5432") {
		t.Fatalf("first forward = %q, expected pod/pg-2", r.forwards[0])
	}
	// a pod that was not ready when it was picked stays while it runs
	r.stream(t, "pg-2").ch <- event{Type: "MODIFIED", Object: object{
		"metadata": map[string]any{"name": "pg-2", "labels": map[string]any{"cnpg.io/cluster": "pg", "cnpg.io/instanceRole": "primary"}},
		"status":   unready,
	}}
	time.Sleep(100 * time.Millisecond)
	if n := r.forwardCount(); n != 1 {
		t.Errorf("forwards = %d, expected 1: %v", n, r.forwards)
	}
	if err := m.Err(); err != nil {
		t.Errorf("Err = %v, expected nil", err)
	}
}
