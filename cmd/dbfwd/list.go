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
	u, err := listURL(arg, otx.Bool(ctx, "all-namespaces"), otx.String(ctx, "namespace"), otx.String(ctx, "context"))
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

// listURL builds the URL of a listing from the scope argument and the flags,
// with dbfwd.ListURL, which the \k command of usql calls too.
func listURL(arg string, all bool, namespace, kubeContext string) (*dburl.URL, error) {
	if all && namespace != "" {
		return nil, fmt.Errorf("listing with -A and -n %s, which disagree: %w", namespace, dbfwd.ErrBadURL)
	}
	if all {
		namespace = "*"
	}
	var where []dbfwd.Field
	if namespace != "" {
		where = append(where, dbfwd.Field{Name: "namespace", Value: namespace})
	}
	if kubeContext != "" {
		where = append(where, dbfwd.Field{Name: "context", Value: kubeContext})
	}
	return dbfwd.ListURL("k8s", arg, where...)
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
