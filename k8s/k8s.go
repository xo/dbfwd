// Package k8s is the Kubernetes backend of dbfwd. It registers itself as
// k8s, and exports nothing else:
//
//	import _ "github.com/xo/dbfwd/k8s"
//
// It reads a URL such as k+cnpg://pg.prod/app or k://pg-rw.prod/, finds the
// database with kubectl, reads its credentials from the cluster, and starts
// kubectl port-forward. docs/URL.md and docs/LOOKUP.md hold the rules.
package k8s

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strconv"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// name is the name of the backend and of its scheme.
const name = "k8s"

func init() {
	dbfwd.Register(name, backend{newRunner: func(cfg dbfwd.Config) runner { return newExecRunner(cfg) }})
}

// backend is the Kubernetes backend.
type backend struct {
	// newRunner builds the runner of one call. A test replaces it.
	newRunner func(dbfwd.Config) runner
}

// Lookup runs the lookup for u. It reads no Secret and starts no forward.
func (b backend) Lookup(ctx context.Context, u *dburl.URL, cfg dbfwd.Config) (*dbfwd.Match, error) {
	l, f, err := b.find(ctx, u, cfg)
	if err != nil {
		return nil, err
	}
	return &dbfwd.Match{Target: f.target, Steps: l.steps}, nil
}

// Open finds the database that u names, reads its credentials and starts
// kubectl port-forward. It returns once the forward is ready.
func (b backend) Open(ctx context.Context, u *dburl.URL, cfg dbfwd.Config) (*dbfwd.Opened, error) {
	l, f, err := b.find(ctx, u, cfg)
	if err != nil {
		return nil, err
	}
	k, req := l.k, l.req
	acct, err := pickAccount(ctx, k, req, f)
	if err != nil {
		return nil, err
	}
	user, err := acct.user.resolve(ctx, k)
	if err != nil {
		return nil, err
	}
	password, err := acct.password.resolve(ctx, k)
	if err != nil {
		return nil, err
	}
	database := req.database
	if database == "" {
		if database, err = f.database.resolve(ctx, k); err != nil {
			return nil, err
		}
	}
	port := f.port
	if req.port != 0 {
		port = req.port
	}
	t, err := k.r.forward(ctx, k.args("port-forward", "--address", "127.0.0.1", f.forward, ":"+strconv.Itoa(port))...)
	if err != nil {
		return nil, fmt.Errorf("forwarding to %s: %w", f.target, err)
	}
	return &dbfwd.Opened{
		URL:      localURL(f.scheme, user, password, t.port(), database, req.query),
		User:     user,
		Database: database,
		Target:   f.target,
		Tunnel:   t,
	}, nil
}

// find parses u and runs the lookup.
func (b backend) find(ctx context.Context, u *dburl.URL, cfg dbfwd.Config) (*lookup, *found, error) {
	req, err := parseRequest(u)
	if err != nil {
		return nil, nil, err
	}
	r := b.newRunner(cfg)
	context, namespace, err := current(ctx, r, req.context)
	if err != nil {
		return nil, nil, err
	}
	if req.namespace != "" {
		namespace = req.namespace
	}
	l := &lookup{
		k:     &kube{r: r, context: req.context, namespace: namespace},
		req:   req,
		scope: []dbfwd.Field{{Name: "context", Value: context}, {Name: "namespace", Value: namespace}},
	}
	f, err := l.run(ctx)
	if err != nil {
		return nil, nil, err
	}
	f.target.Backend = name
	f.target.Scope = l.scope
	if f.target.Type == "" {
		f.target.Type = f.scheme
	}
	return l, f, nil
}

// pickAccount returns the account that the user part asks for (D6).
func pickAccount(ctx context.Context, k *kube, req request, f *found) (account, error) {
	switch {
	case req.secretForm:
		data, err := k.secret(ctx, req.secret)
		if err != nil {
			return account{}, err
		}
		v, ok := data[req.field]
		if !ok {
			return account{}, fmt.Errorf("reading the key %s of the secret %s: %w", req.field, req.secret, dbfwd.ErrNotFound)
		}
		acct := account{user: f.account.user, password: literal(v)}
		if u, ok := data["username"]; ok {
			acct.user = literal(u)
		}
		return acct, nil
	case req.userSet:
		if acct, ok := f.users[req.user]; ok {
			return acct, nil
		}
		return account{}, fmt.Errorf("finding the user %s in %s: %w", req.user, f.target, dbfwd.ErrNotFound)
	}
	if !f.account.password.known() && !f.account.user.known() {
		return account{}, fmt.Errorf("finding the credentials of %s: %w", f.target, dbfwd.ErrNotFound)
	}
	return f.account, nil
}

// localURL builds the URL of the local end.
func localURL(scheme, user, password string, port int, database string, query url.Values) string {
	u := url.URL{Scheme: scheme, Host: "127.0.0.1:" + strconv.Itoa(port)}
	switch {
	case user != "" && password != "":
		u.User = url.UserPassword(user, password)
	case user != "":
		u.User = url.User(user)
	}
	q := url.Values{}
	maps.Copy(q, query)
	if database != "" {
		if scheme == "sqlserver" {
			q.Set("database", database)
		} else {
			u.Path = "/" + database
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
