package k8s

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// The query keys that the backend owns. Every other key goes to the driver.
const (
	keyContext = "context"
	keyRole    = "role"
)

// The roles that the role key takes.
const (
	roleRW = "rw"
	roleRO = "ro"
)

// request is what a URL asks for.
type request struct {
	// name and namespace come from the host. namespace is empty when the URL
	// gives none.
	name      string
	namespace string

	// typ is the operator or the database type after the +, in lower case.
	typ string

	// context and role come from the query.
	context string
	role    string

	// The user part. With secretForm, secret and field name a Secret and its
	// key. Otherwise user is a database user, when userSet.
	user       string
	userSet    bool
	secretForm bool
	secret     string
	field      string

	// port is the remote port, or zero.
	port int

	// database is the path, or empty.
	database string

	// query holds the keys for the driver.
	query url.Values
}

// parseRequest reads what u asks for. Its errors never hold the URL.
func parseRequest(u *dburl.URL) (request, error) {
	var req request
	host := u.Hostname()
	if host == "" {
		return req, fmt.Errorf("reading the host: %w", dbfwd.ErrBadURL)
	}
	// a namespace is a DNS label and holds no dot, so the host splits at the
	// last dot
	if name, namespace, ok := strings.CutLast(host, "."); ok {
		req.name, req.namespace = name, namespace
	} else {
		req.name = host
	}
	if req.name == "" || (strings.Contains(host, ".") && req.namespace == "") {
		return req, fmt.Errorf("reading the host %q: %w", host, dbfwd.ErrBadURL)
	}
	req.typ = strings.ToLower(u.Transport)
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return req, fmt.Errorf("reading the port %q: %w", p, dbfwd.ErrBadURL)
		}
		req.port = n
	}
	req.database = strings.TrimPrefix(u.Path, "/")
	if u.User != nil {
		name := u.User.Username()
		field, hasField := u.User.Password()
		switch {
		case hasField:
			req.secretForm, req.secret, req.field = true, name, field
			if req.field == "" {
				req.field = "password"
			}
			if req.secret == "" {
				return req, fmt.Errorf("reading the user part: a secret needs a name: %w", dbfwd.ErrBadURL)
			}
		case name != "":
			req.user, req.userSet = name, true
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return req, fmt.Errorf("reading the query: %w", dbfwd.ErrBadURL)
	}
	req.context = q.Get(keyContext)
	req.role = strings.ToLower(q.Get(keyRole))
	switch req.role {
	case "", roleRW, roleRO:
	default:
		return req, fmt.Errorf("reading role %q, which must be rw or ro: %w", req.role, dbfwd.ErrBadURL)
	}
	q.Del(keyContext)
	q.Del(keyRole)
	req.query = q
	return req, nil
}
