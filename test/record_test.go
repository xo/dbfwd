package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xo/dbfwd"
)

// expect is what a replay test of the root module must find for a recorded
// case. It holds no password.
type expect struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	User      string `json:"user"`
	Database  string `json:"database"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	Type      string `json:"type"`
}

// writeExpect writes expect.json for a recorded case.
func writeExpect(t *testing.T, dir, raw string, fwd *dbfwd.Forward) {
	t.Helper()
	ns := ""
	for _, f := range fwd.Target.Scope {
		if f.Name == "namespace" {
			ns = f.Value
		}
	}
	e := expect{
		URL:       strings.ReplaceAll(raw, c.context, "CONTEXT"),
		Namespace: ns,
		User:      fwd.User,
		Database:  fwd.Database,
		Kind:      fwd.Target.Kind,
		Name:      fwd.Target.Name,
		Owner:     fwd.Target.Owner,
		Type:      fwd.Target.Type,
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "expect.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
