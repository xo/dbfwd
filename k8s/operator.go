package k8s

import (
	"fmt"
	"slices"
	"strings"

	"github.com/xo/dbfwd"
)

// operator holds the rules of one operator. Each rule was read from the
// upstream source at the release that docs/OPERATORS.md names (D9).
type operator struct {
	// name is the name a person reads, such as CloudNativePG.
	name string

	// key is the operator name in a URL, such as cnpg.
	key string

	// kind and groups name the cluster CR, and resource is what kubectl get
	// takes.
	kind     string
	groups   []string
	resource string

	// build reads a cluster CR for the role, rw or ro.
	build func(cr object, role string) (*found, error)

	// roleOf returns the role that a Service of the cluster implies, or
	// empty.
	roleOf func(cluster, service string) string
}

// owns reports whether the operator manages objects of the kind and group.
func (op *operator) owns(kind, group string) bool {
	return kind == op.kind && slices.Contains(op.groups, group)
}

// operators is every supported operator with rules. The order is the order of
// the first step of the lookup.
var operators = []*operator{cnpg, mariadbOperator, moco, everest}

// operatorFor returns the operator with the key, such as cnpg.
func operatorFor(key string) *operator {
	for _, op := range operators {
		if op.key == key {
			return op
		}
	}
	return nil
}

// perconaKinds are the CRs that OpenEverest makes. Each one has the name of
// its DatabaseCluster, and no owner reference to it.
var perconaKinds = map[string]string{
	"PerconaXtraDBCluster": "pxc.percona.com",
	"PerconaPGCluster":     "pgv2.percona.com",
	"PostgresCluster":      "upstream.pgv2.percona.com",
}

// pick returns ro when role is ro, and rw otherwise.
func pick(role string) string {
	if role == roleRO {
		return roleRO
	}
	return roleRW
}

// cnpg is CloudNativePG, read at v1.30.1.
var cnpg = &operator{
	name:     "CloudNativePG",
	key:      "cnpg",
	kind:     "Cluster",
	groups:   []string{"postgresql.cnpg.io"},
	resource: "clusters.postgresql.cnpg.io",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		db := cr.str("spec", "bootstrap", "initdb", "database")
		if db == "" {
			db = "app"
		}
		owner := cr.str("spec", "bootstrap", "initdb", "owner")
		if owner == "" {
			owner = db
		}
		if pick(role) == roleRO && cr.num("spec", "instances") < 2 {
			return nil, fmt.Errorf("reaching a replica of the CloudNativePG cluster %s, which has one instance: %w", name, dbfwd.ErrNotFound)
		}
		appSecret := cr.str("spec", "bootstrap", "initdb", "secret", "name")
		if appSecret == "" {
			appSecret = name + "-app"
		}
		f := &found{
			scheme:   "postgres",
			forward:  "service/" + name + "-" + pick(role),
			port:     5432,
			role:     pick(role),
			account:  account{user: literal(owner), password: fromSecret(appSecret, "password")},
			users:    map[string]account{},
			database: literal(db),
		}
		f.users[owner] = f.account
		if cr.boolean("spec", "enableSuperuserAccess") {
			su := cr.str("spec", "superuserSecret", "name")
			if su == "" {
				su = name + "-superuser"
			}
			f.users["postgres"] = account{user: literal("postgres"), password: fromSecret(su, "password")}
		}
		for _, r := range cr.list("spec", "managed", "roles") {
			if s := r.str("passwordSecret", "name"); s != "" {
				f.users[r.str("name")] = account{user: literal(r.str("name")), password: fromSecret(s, "password")}
			}
		}
		return f, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster + "-rw":
			return roleRW
		case cluster + "-ro":
			return roleRO
		}
		return ""
	},
}

// mariadbOperator is mariadb-operator, read at 26.10.2.
var mariadbOperator = &operator{
	name:     "mariadb-operator",
	key:      "mariadb-operator",
	kind:     "MariaDB",
	groups:   []string{"k8s.mariadb.com", "mariadb.mmontes.io"},
	resource: "mariadbs.k8s.mariadb.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		ha := cr.boolean("spec", "replication", "enabled") || cr.boolean("spec", "galera", "enabled")
		service := name
		switch {
		case ha && pick(role) == roleRO:
			service = name + "-secondary"
		case ha:
			service = name + "-primary"
		}
		port := cr.num("spec", "port")
		if port == 0 {
			port = 3306
		}
		root := account{user: literal("root"), password: keyRef(cr, name+"-root", "spec", "rootPasswordSecretKeyRef")}
		f := &found{
			scheme:  "mariadb",
			forward: "service/" + service,
			port:    port,
			role:    pick(role),
			account: root,
			users:   map[string]account{"root": root},
		}
		if db := cr.str("spec", "database"); db != "" {
			f.database = literal(db)
		}
		if u := cr.str("spec", "username"); u != "" {
			f.account = account{user: literal(u), password: keyRef(cr, name+"-password", "spec", "passwordSecretKeyRef")}
			f.users[u] = f.account
		}
		return f, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster + "-primary":
			return roleRW
		case cluster + "-secondary":
			return roleRO
		}
		return ""
	},
}

// keyRef reads a SecretKeySelector at path, with the default name and the
// key password.
func keyRef(cr object, name string, path ...string) credential {
	key := cr.str(append(path, "key")...)
	if key == "" {
		key = "password"
	}
	if n := cr.str(append(path, "name")...); n != "" {
		name = n
	}
	return fromSecret(name, key)
}

// moco is MOCO, read at v0.38.0. MOCO has no application user, so
// moco-writable is the default (D9).
var moco = &operator{
	name:     "MOCO",
	key:      "moco",
	kind:     "MySQLCluster",
	groups:   []string{"moco.cybozu.com"},
	resource: "mysqlclusters.moco.cybozu.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := "moco-" + name
		service := "moco-" + name + "-primary"
		if pick(role) == roleRO {
			service = "moco-" + name + "-replica"
		}
		f := &found{
			scheme:  "mysql",
			forward: "service/" + service,
			port:    3306,
			role:    pick(role),
			users: map[string]account{
				"moco-writable": {user: literal("moco-writable"), password: fromSecret(secret, "WRITABLE_PASSWORD")},
				"moco-readonly": {user: literal("moco-readonly"), password: fromSecret(secret, "READONLY_PASSWORD")},
				"moco-admin":    {user: literal("moco-admin"), password: fromSecret(secret, "ADMIN_PASSWORD")},
			},
		}
		f.account = f.users["moco-writable"]
		return f, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case "moco-" + cluster + "-primary":
			return roleRW
		case "moco-" + cluster + "-replica":
			return roleRO
		}
		return ""
	},
}

// everest is OpenEverest, read at operator v1.17.0. Its pxc engine is
// supported. Its psmdb engine is MongoDB, and the keys of its postgresql
// Secret are not read yet.
var everest = &operator{
	name:     "OpenEverest",
	key:      "everest",
	kind:     "DatabaseCluster",
	groups:   []string{"everest.percona.com"},
	resource: "databaseclusters.everest.percona.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := cr.str("spec", "engine", "userSecretsName")
		if secret == "" {
			secret = "everest-secrets-" + name
		}
		switch engine := cr.str("spec", "engine", "type"); engine {
		case "pxc":
			service := cr.str("status", "hostname")
			if i := strings.IndexByte(service, '.'); i >= 0 {
				service = service[:i]
			}
			if service == "" {
				service = name + "-haproxy"
			}
			port := cr.num("status", "port")
			if port == 0 {
				port = 3306
			}
			root := account{user: literal("root"), password: fromSecret(secret, "root")}
			return &found{
				scheme:  "mysql",
				forward: "service/" + service,
				port:    port,
				role:    pick(role),
				account: root,
				users:   map[string]account{"root": root},
			}, nil
		case "psmdb":
			return nil, fmt.Errorf("reading the OpenEverest cluster %s, which is MongoDB: %w", name, dbfwd.ErrUnsupported)
		default:
			return nil, fmt.Errorf("reading the OpenEverest cluster %s with the engine %q, whose secret keys are not read yet: %w", name, engine, dbfwd.ErrUnsupported)
		}
	},
	roleOf: func(cluster, service string) string {
		if service == cluster+"-haproxy" {
			return roleRW
		}
		return ""
	},
}
