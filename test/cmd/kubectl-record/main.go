// Command kubectl-record runs kubectl and records each read for the replay
// tests of dbfwd.
//
// It runs the kubectl on the PATH with its own arguments. For kubectl get and
// kubectl api-resources, it also appends the call, its output and its exit
// status to calls.jsonl in DBFWD_RECORD_DIR, with the context named in
// DBFWD_RECORD_CONTEXT written as CONTEXT. Before it writes, it replaces each
// value of each Secret with a fake one, fake-<key>, apart from a key that holds
// a name, such as username, and drops the fields that differ on every cluster. It records nothing else, and it passes a
// port-forward straight to kubectl.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Call is one recorded call of kubectl.
type Call struct {
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr,omitempty"`
	Exit   int      `json:"exit,omitempty"`
}

func main() {
	args := os.Args[1:]
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubectl-record: finding kubectl:", err)
		os.Exit(127)
	}
	if !slices.Contains(args, "get") && !slices.Contains(args, "api-resources") {
		// a port-forward and anything else run as kubectl itself
		err := syscall.Exec(kubectl, append([]string{"kubectl"}, args...), os.Environ())
		fmt.Fprintln(os.Stderr, "kubectl-record: running kubectl:", err)
		os.Exit(127)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(kubectl, args...) //nolint:noctx // kubectl runs to the end, as the caller asked
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, "kubectl-record: running kubectl:", err)
			os.Exit(127)
		}
		code = exitErr.ExitCode()
	}
	_, _ = os.Stdout.Write(stdout.Bytes())
	_, _ = os.Stderr.Write(stderr.Bytes())
	if err := record(args, stdout.Bytes(), stderr.String(), code); err != nil {
		fmt.Fprintln(os.Stderr, "kubectl-record: recording:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// record appends one call to calls.jsonl.
func record(args []string, stdout []byte, stderr string, code int) error {
	dir := os.Getenv("DBFWD_RECORD_DIR")
	if dir == "" {
		return nil
	}
	context := os.Getenv("DBFWD_RECORD_CONTEXT")
	norm := make([]string, len(args))
	for i, a := range args {
		norm[i] = a
		if context != "" && a == context {
			norm[i] = "CONTEXT"
		}
	}
	out := string(stdout)
	if len(bytes.TrimSpace(stdout)) != 0 && stdout[0] == '{' {
		var o map[string]any
		if err := json.Unmarshal(stdout, &o); err != nil {
			return err
		}
		scrub(o)
		b, err := json.Marshal(o)
		if err != nil {
			return err
		}
		out = string(b)
	}
	line, err := json.Marshal(Call{Args: norm, Stdout: out, Stderr: stderr, Exit: code})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// plain reports whether a key of a Secret holds a name and not a credential,
// such as username, POSTGRES_USER or dbname. A key that names a password, a
// token, a key or a URI is never plain.
func plain(key string) bool {
	k := strings.ToLower(key)
	for _, w := range []string{"pass", "token", "key", "secret", "uri", "cert", "auth", "pgpass", "jdbc"} {
		if strings.Contains(k, w) {
			return false
		}
	}
	for _, suffix := range []string{"user", "username", "dbname", "database", "_db", "host", "port"} {
		if k == strings.TrimPrefix(suffix, "_") || strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// fakeJSON keeps the structure of a value that is JSON, such as the
// users.json file of vtgate, and replaces each string under a key that names
// a password or a token with fake-<key>. It reports false for a value that is
// not a JSON object.
func fakeJSON(raw []byte) (string, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	if _, ok := v.(map[string]any); !ok {
		return "", false
	}
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				lower := strings.ToLower(k)
				if s, ok := x.(string); ok && s != "" && (strings.Contains(lower, "pass") || strings.Contains(lower, "token")) {
					t[k] = "fake-" + k
					continue
				}
				t[k] = walk(x)
			}
		case []any:
			for i, x := range t {
				t[i] = walk(x)
			}
		}
		return v
	}
	b, err := json.Marshal(walk(v))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// scrub replaces each value of a Secret with fake-<key>, and drops the fields
// that differ on every cluster. It walks a List too.
func scrub(o map[string]any) {
	if items, ok := o["items"].([]any); ok {
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				scrub(m)
			}
		}
	}
	if o["kind"] == "Secret" {
		for _, field := range []string{"data", "stringData"} {
			if d, ok := o[field].(map[string]any); ok {
				for k, v := range d {
					if plain(k) {
						continue
					}
					fake := "fake-" + k
					if raw, err := base64.StdEncoding.DecodeString(fmt.Sprint(v)); err == nil {
						if j, ok := fakeJSON(raw); ok {
							fake = j
						}
					}
					d[k] = base64.StdEncoding.EncodeToString([]byte(fake))
				}
			}
		}
		if field, ok := o["stringData"].(map[string]any); ok {
			for k := range field {
				if !plain(k) {
					field[k] = "fake-" + k
				}
			}
		}
	}
	if m, ok := o["metadata"].(map[string]any); ok {
		for _, k := range []string{"managedFields", "uid", "resourceVersion", "creationTimestamp", "generation", "selfLink"} {
			delete(m, k)
		}
		if a, ok := m["annotations"].(map[string]any); ok {
			delete(a, "kubectl.kubernetes.io/last-applied-configuration")
		}
		if refs, ok := m["ownerReferences"].([]any); ok {
			for _, r := range refs {
				if rm, ok := r.(map[string]any); ok {
					delete(rm, "uid")
				}
			}
		}
	}
	switch o["kind"] {
	case "Pod":
		// a pod keeps its phase and its Ready condition, which pick a ready pod
		if st, ok := o["status"].(map[string]any); ok {
			status := map[string]any{"phase": st["phase"]}
			conds, _ := st["conditions"].([]any)
			for _, v := range conds {
				if cond, ok := v.(map[string]any); ok && cond["type"] == "Ready" {
					status["conditions"] = []any{map[string]any{"type": "Ready", "status": cond["status"]}}
				}
			}
			o["status"] = status
		}
	case "DatabaseCluster":
		// an OpenEverest cluster keeps its hostname and port
		if st, ok := o["status"].(map[string]any); ok {
			o["status"] = map[string]any{"hostname": st["hostname"], "port": st["port"]}
		}
	case "VitessCluster":
		// a VitessCluster keeps the name of its vtgate Service
		if st, ok := o["status"].(map[string]any); ok {
			o["status"] = map[string]any{"gatewayServiceName": st["gatewayServiceName"]}
		}
	default:
		delete(o, "status")
	}
}
