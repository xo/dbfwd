package k8s

import (
	"bufio"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xo/dbfwd"
)

// The replay tests feed dbfwd the kubectl output that the cluster tests in
// ../test recorded with -update (D12). Every Secret value there is fake-<key>.

// recorded is one recorded call of kubectl.
type recorded struct {
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
}

// expected is what a recorded case must find.
type expected struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	User      string `json:"user"`
	Database  string `json:"database"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Type      string `json:"type"`
}

// replayRunner answers kubectl from the recorded calls of one case.
type replayRunner struct {
	t         *testing.T
	namespace string
	calls     map[string]recorded
}

func (r *replayRunner) get(_ context.Context, args ...string) ([]byte, error) {
	if isConfig(args) {
		return json.Marshal(map[string]any{
			"current-context": "CONTEXT",
			"contexts": []any{map[string]any{
				"name": "CONTEXT", "context": map[string]any{"namespace": r.namespace},
			}},
		})
	}
	c, ok := r.calls[strings.Join(args, " ")]
	if !ok {
		// a call that the recording does not hold answers not found, as a
		// missing object does. The checks of the case still say whether
		// dbfwd found the right database.
		r.t.Logf("no recorded call for kubectl %s", strings.Join(args, " "))
		return nil, errNotFound
	}
	if c.Exit != 0 {
		return nil, classifyExit(c.Exit, c.Stderr)
	}
	return []byte(c.Stdout), nil
}

func (r *replayRunner) watch(...string) (stream, error) {
	return &fakeStream{ch: make(chan event)}, nil
}

func (r *replayRunner) forward(context.Context, ...string) (tunnel, error) {
	return &fakeTunnel{done: make(chan struct{})}, nil
}

func TestReplay(t *testing.T) {
	t.Parallel()
	dirs, err := filepath.Glob(filepath.Join("..", "testdata", "*", "*", "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Skip("no recorded cases in ../testdata")
	}
	for _, path := range dirs {
		dir := filepath.Dir(path)
		name := filepath.Base(filepath.Dir(dir)) + "/" + filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var want expected
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			r := &replayRunner{t: t, namespace: want.Namespace, calls: readCalls(t, filepath.Join(dir, "calls.jsonl"))}
			b2 := backend{newRunner: func(dbfwd.Config) runner { return r }}
			o, err := b2.Open(t.Context(), parse(t, want.URL), dbfwd.Config{})
			if err != nil {
				t.Fatalf("Open(%s) error = %v", want.URL, err)
			}
			if o.User != want.User || o.Database != want.Database {
				t.Errorf("user, database = %q, %q, expected %q, %q", o.User, o.Database, want.User, want.Database)
			}
			tg := o.Target
			if tg.Kind != want.Kind || tg.Name != want.Name || tg.Owner != want.Owner || tg.Type != want.Type {
				t.Errorf("target = %+v, expected %s %s, owner %q, type %s", tg, want.Kind, want.Name, want.Owner, want.Type)
			}
			u, err := url.Parse(o.URL)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := u.User.Password(); ok && !strings.HasPrefix(p, "fake-") {
				t.Errorf("password = %q, expected a fake one from the recording", p)
			}
		})
	}
}

// readCalls reads the recorded calls of a case, by their arguments.
func readCalls(t *testing.T, path string) map[string]recorded {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	calls := map[string]recorded{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var c recorded
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls[strings.Join(c.Args, " ")] = c
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}
