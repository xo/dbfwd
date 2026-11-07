package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/xo/dburl"
	"github.com/xo/ox/otx"

	"github.com/xo/dbfwd"
)

// list lists the databases of a scope, as the \k command of usql does
// (docs/LIST.md).
func list(ctx context.Context, args []string) error {
	arg := ""
	if len(args) != 0 {
		arg = args[0]
	}
	raw, err := scopeURL(arg, otx.Bool(ctx, "all-namespaces"), otx.String(ctx, "namespace"), otx.String(ctx, "context"))
	if err != nil {
		return err
	}
	u, err := parseURL(raw)
	if err != nil {
		return err
	}
	l, err := dbfwd.List(ctx, u, options(ctx)...)
	if err != nil {
		return err
	}
	printListing(os.Stdout, os.Stderr, l, otx.Bool(ctx, "wide"))
	return nil
}

// scopeURL builds the URL of a listing from the scope argument and the flags.
// The k:// is optional: * becomes k://*, and cnpg://* becomes k+cnpg://*. A
// URL whose scheme is already k8s, such as kube:// or k+cnpg://, stays as it
// is. A flag that disagrees with the URL is an error.
func scopeURL(arg string, all bool, namespace, kubeContext string) (string, error) {
	switch {
	case arg == "":
		arg = "k://*"
	case !strings.Contains(arg, "://"):
		arg = "k://" + arg
	default:
		if u, err := dburl.Parse(arg); err != nil || u.SchemeName != "k8s" {
			arg = "k+" + arg
		}
	}
	if all && namespace != "" {
		return "", fmt.Errorf("listing with -A and -n %s, which disagree: %w", namespace, dbfwd.ErrBadURL)
	}
	u, err := dburl.Parse(arg)
	if err != nil {
		return "", fmt.Errorf("reading the scope: %w", dbfwd.ErrBadURL)
	}
	// the host splits at its last dot, as for Resolve
	name, ns, ok := strings.CutLast(u.Host, ".")
	if !ok {
		name, ns = u.Host, ""
	}
	want := namespace
	if all {
		want = "*"
	}
	switch {
	case want == "":
	case ns != "" && ns != want:
		return "", fmt.Errorf("listing the namespace %s, which the flags give as %s: %w", ns, want, dbfwd.ErrBadURL)
	default:
		ns = want
	}
	host := name
	if ns != "" {
		host += "." + ns
	}
	query := u.RawQuery
	if kubeContext != "" {
		q := u.Query()
		if c := q.Get("context"); c != "" && c != kubeContext {
			return "", fmt.Errorf("listing in the context %s, which the flags give as %s: %w", c, kubeContext, dbfwd.ErrBadURL)
		}
		q.Set("context", kubeContext)
		query = q.Encode()
	}
	// the scheme stays as it was written, because dburl drops the part after
	// the + from the URL it rebuilds
	scheme, _, _ := strings.Cut(arg, "://")
	out := scheme + "://" + host + u.Path
	if query != "" {
		out += "?" + query
	}
	return out, nil
}

// printListing prints one row for each entry, and then what the listing
// skipped, to errw.
func printListing(w, errw io.Writer, l *dbfwd.Listing, wide bool) {
	if len(l.Entries) == 0 {
		fmt.Fprintln(errw, "no databases in", scopeString(l.Scope))
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		head := []string{"NAMESPACE", "NAME", "TYPE", "KIND", "OPERATOR", "URL"}
		if wide {
			head = append(head, "OWNER", "ROLES", "READY", "DATABASES", "CREDENTIAL")
		}
		fmt.Fprintln(tw, strings.Join(head, "\t"))
		for _, e := range l.Entries {
			cols := []string{namespaceOf(e.Target.Scope), e.Target.Name, e.Target.Type, e.Target.Kind, dash(e.Operator), e.URL}
			if wide {
				cols = append(cols,
					dash(e.Target.Owner),
					dash(strings.Join(e.Roles, ",")),
					strconv.Itoa(e.Ready)+"/"+strconv.Itoa(e.Pods),
					dash(strings.Join(e.Databases, ",")),
					dash(e.Credential),
				)
			}
			fmt.Fprintln(tw, strings.Join(cols, "\t"))
		}
		_ = tw.Flush()
	}
	for _, s := range l.Skipped {
		fmt.Fprintf(errw, "dbfwd: skipped %s in %s: %s\n", s.Kind, scopeString(s.Scope), s.Reason)
	}
}

// namespaceOf returns the namespace of a scope.
func namespaceOf(scope []dbfwd.Field) string {
	for _, f := range scope {
		if f.Name == "namespace" {
			return f.Value
		}
	}
	return ""
}

// scopeString returns a scope as text, such as "context kind-prod, namespace
// prod". Every namespace reads as "every namespace".
func scopeString(scope []dbfwd.Field) string {
	parts := make([]string, 0, len(scope))
	for _, f := range scope {
		switch {
		case f.Value == "":
		case f.Name == "namespace" && f.Value == "*":
			parts = append(parts, "every namespace")
		default:
			parts = append(parts, f.Name+" "+f.Value)
		}
	}
	return strings.Join(parts, ", ")
}

// dash returns s, or - when s is empty.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
