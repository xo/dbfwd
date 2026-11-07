package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/xo/dbfwd"
)

// oracleMySQL is the Oracle MySQL Operator, read at 26.10.0-2.3.1. The Secret
// that the person names holds the user too, in rootUser. The Router Service
// serves read-write on 3306 and read-only on 6447.
var oracleMySQL = &operator{
	name:     "MySQL Operator",
	key:      "mysql-operator",
	kind:     "InnoDBCluster",
	groups:   []string{"mysql.oracle.com"},
	resource: "innodbclusters.mysql.oracle.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := cr.str("spec", "secretName")
		if secret == "" {
			return nil, fmt.Errorf("reading the secret of the InnoDBCluster %s, which names none: %w", name, dbfwd.ErrNotFound)
		}
		user := fromSecret(secret, "rootUser")
		user.optional, user.fallback = true, &credential{value: "root", set: true}
		root := account{user: user, password: fromSecret(secret, "rootPassword")}
		port := 3306
		if pick(role) == roleRO {
			port = 6447
		}
		return &found{
			scheme:  "mysql",
			forward: "service/" + name,
			port:    port,
			role:    pick(role),
			account: root,
			users:   map[string]account{"root": root},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// perconaXtraDB is the Percona Operator for MySQL based on Percona XtraDB
// Cluster, read at v1.20.0. OpenEverest pxc makes one of these, with the same
// name and its own Secret, so the rule serves both.
var perconaXtraDB = &operator{
	name:     "Percona XtraDB Cluster",
	key:      "pxc",
	kind:     "PerconaXtraDBCluster",
	groups:   []string{"pxc.percona.com"},
	resource: "perconaxtradbclusters.pxc.percona.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := orDefault(cr.str("spec", "secretsName"), name+"-secrets")
		service := name + "-haproxy"
		switch {
		case !cr.boolean("spec", "haproxy", "enabled") && cr.boolean("spec", "proxysql", "enabled"):
			service = name + "-proxysql"
		case pick(role) == roleRO:
			service = name + "-haproxy-replicas"
		}
		root := account{user: literal("root"), password: fromSecret(secret, "root")}
		return &found{
			scheme:  "mysql",
			forward: "service/" + service,
			port:    3306,
			role:    pick(role),
			account: root,
			users:   map[string]account{"root": root},
		}, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster + "-haproxy", cluster + "-proxysql":
			return roleRW
		case cluster + "-haproxy-replicas":
			return roleRO
		}
		return ""
	},
}

// perconaServer is the Percona Operator for MySQL based on Percona Server,
// read at v1.3.0. HAProxy serves read-write on 3306 and read-only on 3307.
var perconaServer = &operator{
	name:     "Percona Server for MySQL",
	key:      "ps",
	kind:     "PerconaServerMySQL",
	groups:   []string{"ps.percona.com"},
	resource: "perconaservermysqls.ps.percona.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := orDefault(cr.str("spec", "secretsName"), name+"-secrets")
		service, port := name+"-haproxy", 3306
		switch {
		case cr.boolean("spec", "proxy", "router", "enabled") && !cr.boolean("spec", "proxy", "haproxy", "enabled"):
			service = name + "-router"
			if pick(role) == roleRO {
				port = 6447
			}
		case pick(role) == roleRO:
			port = 3307
		}
		root := account{user: literal("root"), password: fromSecret(secret, "root")}
		return &found{
			scheme:  "mysql",
			forward: "service/" + service,
			port:    port,
			role:    pick(role),
			pods:    "app.kubernetes.io/instance=" + name + ",mysql.percona.com/primary=true",
			account: root,
			users:   map[string]account{"root": root},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// tidb is TiDB Operator, read at v1.6.6. Every TiDB pod is a SQL node on
// port 4000. The Service of TiDB exists only when the CR asks for it, so a
// cluster without one forwards to a TiDB pod.
var tidb = &operator{
	name:     "TiDB Operator",
	key:      "tidb-operator",
	kind:     "TidbCluster",
	groups:   []string{"pingcap.com"},
	resource: "tidbclusters.pingcap.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		root := account{user: literal("root")}
		if cr.boolean("spec", "tidb", "initializer", "createPassword") {
			root.password = fromSecret(name+"-init", "root")
		}
		return &found{
			scheme:  "tidb",
			forward: "service/" + name + "-tidb",
			port:    4000,
			role:    pick(role),
			pods:    "app.kubernetes.io/component=tidb,app.kubernetes.io/instance=" + name,
			account: root,
			users:   map[string]account{"root": root},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// vitess is the Vitess Operator, read at v2.17.1. vtgate reads its users from
// a users.json file in a Secret that the person names, and the keyspace is
// the database.
var vitess = &operator{
	name:     "Vitess Operator",
	key:      "vitess-operator",
	kind:     "VitessCluster",
	groups:   []string{"planetscale.com"},
	resource: "vitessclusters.planetscale.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		service := cr.str("status", "gatewayServiceName")
		if service == "" {
			return nil, fmt.Errorf("reading the vtgate service of the VitessCluster %s, which has none yet: %w", name, dbfwd.ErrNotFound)
		}
		f := &found{
			scheme:  "vitess",
			forward: "service/" + service,
			port:    3306,
			role:    pick(role),
			users:   map[string]account{},
		}
		for _, cell := range cr.list("spec", "cells") {
			secret := cell.str("gateway", "authentication", "static", "secret", "name")
			if secret == "" {
				continue
			}
			key := orDefault(cell.str("gateway", "authentication", "static", "secret", "key"), "users.json")
			user := fromSecret(secret, key)
			user.transform = func(s string) (string, error) { u, _, err := vtgateUser(s); return u, err }
			password := fromSecret(secret, key)
			password.transform = func(s string) (string, error) { _, p, err := vtgateUser(s); return p, err }
			f.account = account{user: user, password: password}
			break
		}
		if ks := cr.list("spec", "keyspaces"); len(ks) != 0 {
			f.database = literal(ks[0].str("name"))
		}
		return f, nil
	},
	roleOf: func(string, string) string { return "" },
}

// vtgateUser returns the first user, by name, of a vtgate users.json file,
// and its password.
func vtgateUser(s string) (string, string, error) {
	var users map[string][]struct {
		Password string `json:"Password"` //nolint:tagliatelle // the key that vtgate names in its users.json file
	}
	if err := json.Unmarshal([]byte(s), &users); err != nil {
		return "", "", errors.New("reading a users.json file")
	}
	names := make([]string, 0, len(users))
	for n := range users {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if len(users[n]) != 0 {
			return n, users[n][0].Password, nil
		}
	}
	return "", "", errors.New("finding a user in a users.json file")
}
