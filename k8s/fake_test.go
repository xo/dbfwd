package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// fakeRunner answers kubectl get from objects held in memory, and records
// every call.
type fakeRunner struct {
	mu sync.Mutex

	// objects holds the JSON of each object as "resource/name", and of each
	// list as "resource?selector".
	objects map[string]any

	// installed holds the custom resources that the cluster has. A resource
	// with a dot that is not here is not installed.
	installed map[string]bool

	// forbidden holds the keys that RBAC refuses.
	forbidden map[string]bool

	// noDiscovery makes kubectl api-resources fail, so that each step asks
	// for itself.
	noDiscovery bool

	// listed holds the objects that a listing reads, by resource, with their
	// kind, apiVersion and namespace.
	listed map[string][]map[string]any

	calls    []string
	forwards []string

	// streams holds each watch that dbfwd started, by its pod.
	streams map[string]*fakeStream
}

// fakeStream is a watch that a test feeds.
type fakeStream struct {
	ch   chan event
	once sync.Once
}

func (s *fakeStream) events() <-chan event { return s.ch }
func (s *fakeStream) stop()                { s.once.Do(func() { close(s.ch) }) }

func (r *fakeRunner) watch(args ...string) (stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pod := ""
	for i, a := range args {
		if a == "pod" && i+1 < len(args) {
			pod = args[i+1]
		}
	}
	s := &fakeStream{ch: make(chan event, 4)}
	if r.streams == nil {
		r.streams = map[string]*fakeStream{}
	}
	r.streams[pod] = s
	return s, nil
}

// stream returns the watch of a pod, once dbfwd started it.
func (r *fakeRunner) stream(t *testing.T, pod string) *fakeStream {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		s := r.streams[pod]
		r.mu.Unlock()
		if s != nil {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no watch of the pod %s", pod)
	return nil
}

// forwardCount returns how many forwards dbfwd started.
func (r *fakeRunner) forwardCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.forwards)
}

func newFake() *fakeRunner {
	return &fakeRunner{
		objects:   map[string]any{},
		installed: map[string]bool{},
		forbidden: map[string]bool{},
	}
}

// add stores an object under resource/name.
func (r *fakeRunner) add(resource string, o map[string]any) {
	name, _ := o["metadata"].(map[string]any)["name"].(string)
	if _, ok := o["status"]; resource == "pod" && !ok {
		// a pod that says nothing of its phase runs
		o["status"] = map[string]any{"phase": "Running"}
	}
	r.objects[resource+"/"+name] = o
	if strings.Contains(resource, ".") && !strings.HasSuffix(resource, ".apps") {
		r.installed[resource] = true
	}
}

// addList stores the items of a list under resource?selector.
func (r *fakeRunner) addList(resource, selector string, items ...map[string]any) {
	list := make([]any, len(items))
	for i, it := range items {
		list[i] = it
	}
	r.objects[resource+"?"+selector] = map[string]any{"items": list}
}

func (r *fakeRunner) get(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, strings.Join(args, " "))
	var rest []string
	namespace := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--namespace":
			namespace = args[i+1]
			i++
		case "--context", "--output":
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if slices.Contains(rest, "--request-timeout") {
		return r.listing(rest[1], namespace, slices.Contains(rest, "--all-namespaces"))
	}
	if len(rest) > 0 && rest[0] == "config" {
		return json.Marshal(map[string]any{
			"current-context": "kind-test",
			"contexts": []any{map[string]any{
				"name":    "kind-test",
				"context": map[string]any{"namespace": "default"},
			}},
		})
	}
	if len(rest) > 0 && rest[0] == "api-resources" {
		if r.noDiscovery {
			return nil, dbfwd.ErrForbidden
		}
		var b strings.Builder
		for _, core := range []string{"services", "pods", "secrets", "statefulsets.apps", "deployments.apps", "replicasets.apps"} {
			b.WriteString(core + "\n")
		}
		for res := range r.installed {
			b.WriteString(res + "\n")
		}
		return []byte(b.String()), nil
	}
	if len(rest) < 2 || rest[0] != "get" {
		return nil, dbfwd.ErrUnsupported
	}
	resource := rest[1]
	key := ""
	switch {
	case len(rest) >= 4 && rest[2] == "--selector":
		key = resource + "?" + rest[3]
	case len(rest) >= 3:
		key = resource + "/" + rest[2]
	}
	if r.forbidden[key] || r.forbidden[resource] {
		return nil, dbfwd.ErrForbidden
	}
	if o, ok := r.objects[key]; ok {
		return json.Marshal(o)
	}
	if strings.Contains(resource, ".") && !strings.HasSuffix(resource, ".apps") && !r.installed[resource] {
		return nil, errNotInstalled
	}
	if resource == "pods" && strings.Contains(key, "?") {
		// pods matching a selector come from the pods that were added
		sel, err := parseSelector(rest[3])
		if err != nil {
			return nil, err
		}
		var items []any
		for k, o := range r.objects {
			if p, ok := strings.CutPrefix(k, "pod/"); ok && p != "" {
				m, _ := o.(map[string]any)
				if sel.matches(object(m).labels()) {
					items = append(items, o)
				}
			}
		}
		return json.Marshal(map[string]any{"items": items})
	}
	if strings.Contains(key, "?") {
		return json.Marshal(map[string]any{"items": []any{}})
	}
	return nil, errNotFound
}

// addListed stores an object for a listing, under its resource.
func (r *fakeRunner) addListed(resource, kind, apiVersion, namespace string, o map[string]any) {
	if r.listed == nil {
		r.listed = map[string][]map[string]any{}
	}
	o["kind"], o["apiVersion"] = kind, apiVersion
	meta, _ := o["metadata"].(map[string]any)
	meta["namespace"] = namespace
	if strings.Contains(resource, ".") && !strings.HasSuffix(resource, ".apps") {
		r.installed[resource] = true
	}
	r.listed[resource] = append(r.listed[resource], o)
}

// listing answers kubectl get of several kinds, in one namespace or in every
// namespace. A forbidden kind fails the whole call, as kubectl does.
func (r *fakeRunner) listing(kinds, namespace string, all bool) ([]byte, error) {
	items := []any{}
	for kind := range strings.SplitSeq(kinds, ",") {
		if r.forbidden[kind] || (all && r.forbidden["--all-namespaces"]) {
			return nil, dbfwd.ErrForbidden
		}
		for _, o := range r.listed[kind] {
			if all || object(o).str("metadata", "namespace") == namespace {
				items = append(items, o)
			}
		}
	}
	return json.Marshal(map[string]any{"items": items})
}

func (r *fakeRunner) forward(_ context.Context, args ...string) (tunnel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forwards = append(r.forwards, strings.Join(args, " "))
	return &fakeTunnel{done: make(chan struct{})}, nil
}

// readSecret reports whether a call read a Secret.
func (r *fakeRunner) readSecret() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.Contains(c, " secret ") {
			return true
		}
	}
	return false
}

// fakeTunnel is a port-forward that is always ready on port 41234.
type fakeTunnel struct {
	done chan struct{}
	once sync.Once
}

func (t *fakeTunnel) Done() <-chan struct{} { return t.done }
func (t *fakeTunnel) Err() error            { return nil }
func (t *fakeTunnel) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
func (t *fakeTunnel) port() int { return 41234 }

// open runs Open of the backend against the fake.
func open(t *testing.T, r *fakeRunner, raw string) (*dbfwd.Opened, error) {
	t.Helper()
	b := backend{newRunner: func(dbfwd.Config) runner { return r }}
	return b.Open(t.Context(), parse(t, raw), dbfwd.Config{})
}

// lookupURL runs Lookup of the backend against the fake.
func lookupURL(t *testing.T, r *fakeRunner, raw string) (*dbfwd.Match, error) {
	t.Helper()
	b := backend{newRunner: func(dbfwd.Config) runner { return r }}
	return b.Lookup(t.Context(), parse(t, raw), dbfwd.Config{})
}

func parse(t *testing.T, raw string) *dburl.URL {
	t.Helper()
	u, err := dburl.Parse(raw)
	if err != nil {
		t.Fatalf("dburl.Parse(%q) error = %v", raw, err)
	}
	return u
}

// meta builds the metadata of an object.
func meta(name string, labels map[string]string, owner ...string) map[string]any {
	m := map[string]any{"name": name}
	if len(labels) != 0 {
		l := map[string]any{}
		for k, v := range labels {
			l[k] = v
		}
		m["labels"] = l
	}
	if len(owner) == 3 {
		m["ownerReferences"] = []any{map[string]any{
			"apiVersion": owner[0], "kind": owner[1], "name": owner[2], "controller": true,
		}}
	}
	return m
}

// secret builds a Secret.
func secret(name string, data map[string]string) map[string]any {
	d := map[string]any{}
	for k, v := range data {
		d[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return map[string]any{"metadata": meta(name, nil), "data": d}
}
