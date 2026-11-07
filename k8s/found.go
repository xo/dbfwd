package k8s

import (
	"context"
	"fmt"

	"github.com/xo/dbfwd"
)

// credential is where a value comes from: a plain value, a key of a Secret,
// or every key of a Secret that matches a name.
type credential struct {
	value  string
	set    bool
	secret string
	key    string

	// optional is set for a key that envFrom can load, and that the Secret
	// may not hold. When the key is missing, fallback gives the value, or the
	// value is empty.
	optional bool
	fallback *credential
}

// literal is a credential with a plain value.
func literal(v string) credential {
	return credential{value: v, set: true}
}

// fromSecret is a credential in a key of a Secret.
func fromSecret(secret, key string) credential {
	return credential{secret: secret, key: key, set: true}
}

// known reports whether the credential has a source.
func (c credential) known() bool {
	return c.set
}

// show returns the value when it is plain, and a description otherwise. It
// never reads a Secret, so Lookup can show it.
func (c credential) show() string {
	switch {
	case !c.set:
		return ""
	case c.secret == "":
		return c.value
	}
	return "from secret " + c.secret + " key " + c.key
}

// resolve reads the value.
func (c credential) resolve(ctx context.Context, k *kube) (string, error) {
	switch {
	case !c.set:
		return "", nil
	case c.secret == "":
		return c.value, nil
	}
	data, err := k.secret(ctx, c.secret)
	if err != nil {
		return "", err
	}
	v, ok := data[c.key]
	switch {
	case !ok && c.optional && c.fallback != nil:
		return c.fallback.resolve(ctx, k)
	case !ok && c.optional:
		return "", nil
	case !ok:
		return "", fmt.Errorf("reading the key %s of the secret %s: %w", c.key, c.secret, dbfwd.ErrNotFound)
	}
	return v, nil
}

// account is a user and the source of its password.
type account struct {
	user     credential
	password credential
}

// found is the database that a lookup found. It holds where the credentials
// are, and never a password.
type found struct {
	target dbfwd.Target

	// scheme is the dburl scheme of the local URL, such as postgres.
	scheme string

	// forward is what kubectl port-forward forwards to, such as
	// service/pg-rw, and port is the remote port.
	forward string
	port    int

	// role is the role that the target implies, or empty.
	role string

	// account is the default account, and users holds the others by name.
	account account
	users   map[string]account

	// database is the default database, or empty.
	database credential
}
