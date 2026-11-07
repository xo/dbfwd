package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// secret is the password in every local URL here. No printed call may hold
// it.
const secret = "s3cr3t-pass"

func local(t *testing.T, raw string) *dburl.URL {
	t.Helper()
	u, err := dburl.Parse(raw)
	if err != nil {
		t.Fatalf("dburl.Parse error = %v", err)
	}
	return u
}

func TestClientCall(t *testing.T) {
	t.Parallel()
	call, err := clientCall("postgres", "", local(t, "postgres://app:"+secret+"@127.0.0.1:41234/app"))
	if err != nil {
		t.Fatalf("clientCall error = %v", err)
	}
	if call.Command != "psql" {
		t.Errorf("command = %q, expected psql", call.Command)
	}
	if slices.ContainsFunc(call.Args, func(a string) bool { return strings.Contains(a, secret) }) {
		t.Errorf("args hold the password: %v", call.Args)
	}
	if !slices.Contains(call.Env, "PGPASSWORD="+secret) {
		t.Errorf("env = %v, expected PGPASSWORD", call.EnvNames())
	}
	var b strings.Builder
	printCall(&b, call)
	if strings.Contains(b.String(), secret) {
		t.Errorf("printed call holds the password: %s", b.String())
	}
	call, err = clientCall("mariadb", "mycli", local(t, "mariadb://shop:"+secret+"@127.0.0.1:41234/shop"))
	if err != nil {
		t.Fatalf("clientCall mariadb error = %v", err)
	}
	if call.Command != "mycli" {
		t.Errorf("command with --client = %q, expected mycli", call.Command)
	}
}

func TestClientCallRefusesAPasswordArgument(t *testing.T) {
	t.Parallel()
	_, err := clientCall("cassandra", "", local(t, "cassandra://app:"+secret+"@127.0.0.1:41234/ks"))
	if !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Fatalf("clientCall cassandra error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error holds the password: %v", err)
	}
	if _, err := clientCall("nosuchdb", "", local(t, "postgres://a@127.0.0.1:1/b")); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("clientCall nosuchdb error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
}

func TestCheckUsqlArgs(t *testing.T) {
	t.Parallel()
	if err := checkUsqlArgs([]string{"-c", "select 1"}); err != nil {
		t.Errorf("checkUsqlArgs(-c) error = %v", err)
	}
	if err := checkUsqlArgs([]string{"postgres://other/db"}); !errors.Is(err, dbfwd.ErrBadURL) {
		t.Errorf("checkUsqlArgs(URL) error = %v, expected %v", err, dbfwd.ErrBadURL)
	}
}

func TestParseURL(t *testing.T) {
	t.Parallel()
	if _, err := parseURL("k+cnpg://pg.prod/"); err != nil {
		t.Errorf("parseURL(k+cnpg) error = %v", err)
	}
	if _, err := parseURL("postgres://pg/"); !errors.Is(err, dbfwd.ErrNoBackend) {
		t.Errorf("parseURL(postgres) error = %v, expected %v", err, dbfwd.ErrNoBackend)
	}
}
