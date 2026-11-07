package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/xo/dbfwd"
)

func TestListURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		arg       string
		all       bool
		namespace string
		context   string
		transport string
		host      string
		query     string
		bad       bool
	}{
		{arg: "", host: "*"},
		{arg: "cnpg://pg*", transport: "cnpg", host: "pg*"},
		{arg: "", all: true, host: "*.*"},
		{arg: "pg*", namespace: "prod", host: "pg*.prod"},
		{arg: "", context: "kind-x", host: "*", query: "context=kind-x"},
		{arg: "", all: true, namespace: "prod", bad: true},
		{arg: "*.prod", all: true, bad: true},
	}
	for _, test := range tests {
		t.Run(test.arg, func(t *testing.T) {
			t.Parallel()
			u, err := listURL(test.arg, test.all, test.namespace, test.context)
			if test.bad {
				if !errors.Is(err, dbfwd.ErrBadURL) {
					t.Fatalf("error = %v, expected %v", err, dbfwd.ErrBadURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if u.SchemeName != "k8s" || u.Transport != test.transport || u.Host != test.host || u.RawQuery != test.query {
				t.Errorf("URL = %s %q %q %q, expected k8s %q %q %q", u.SchemeName, u.Transport, u.Host, u.RawQuery, test.transport, test.host, test.query)
			}
		})
	}
}

func TestPrintListing(t *testing.T) {
	t.Parallel()
	scope := []dbfwd.Field{{Name: "context", Value: "kind-prod"}, {Name: "namespace", Value: "prod"}}
	var out, errOut bytes.Buffer
	printListing(&out, &errOut, &dbfwd.Listing{Scope: scope}, false)
	if got, want := errOut.String(), "no databases in context kind-prod, namespace prod\n"; got != want {
		t.Errorf("empty listing = %q, expected %q", got, want)
	}
	out.Reset()
	errOut.Reset()
	l := &dbfwd.Listing{
		Scope: scope,
		Entries: []dbfwd.Entry{{
			Target:   dbfwd.Target{Scope: scope, Kind: "Cluster", Name: "pg", Owner: "CloudNativePG Cluster pg", Type: "postgres"},
			Operator: "CloudNativePG", URL: "k://pg.prod/", Roles: []string{"rw", "ro"}, Ready: 2, Pods: 3,
			Databases: []string{"app"}, Credential: "Secret pg-app key password",
		}},
		Skipped: []dbfwd.Skip{{Scope: scope, Kind: "pods", Reason: "not allowed"}},
	}
	printListing(&out, &errOut, l, true)
	for _, want := range []string{"NAMESPACE", "CREDENTIAL", "k://pg.prod/", "rw,ro", "2/3", "Secret pg-app key password"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q holds no %q", out.String(), want)
		}
	}
	if got, want := errOut.String(), "dbfwd: skipped pods in context kind-prod, namespace prod: not allowed\n"; got != want {
		t.Errorf("skipped = %q, expected %q", got, want)
	}
}
