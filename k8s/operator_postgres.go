package k8s

import (
	"sort"
	"strings"
)

// zalando is the Zalando postgres-operator, read at v2.0.3. Its read-write
// Service has no selector, and it sets no owner reference by default, so the
// labels of its pods name the cluster.
var zalando = &operator{
	name:     "Zalando postgres-operator",
	key:      "zalando",
	kind:     "postgresql",
	groups:   []string{"acid.zalan.do"},
	resource: "postgresqls.acid.zalan.do",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := func(user string) string {
			return strings.ReplaceAll(user, "_", "-") + "." + name + ".credentials.postgresql.acid.zalan.do"
		}
		service := name
		if pick(role) == roleRO {
			service = name + "-repl"
		}
		f := &found{
			scheme:   "postgres",
			forward:  "service/" + service,
			port:     5432,
			role:     pick(role),
			pods:     "cluster-name=" + name + ",spilo-role=" + roleLabel(role, "master", "replica"),
			users:    map[string]account{},
			database: literal("postgres"),
		}
		f.users["postgres"] = account{user: literal("postgres"), password: fromSecret(secret("postgres"), "password")}
		if m, ok := cr.get("spec", "users").(map[string]any); ok {
			for user := range m {
				f.users[user] = account{user: literal(user), password: fromSecret(secret(user), "password")}
			}
		}
		f.account = f.users["postgres"]
		// the first database by name, and its owner, are the default
		dbs := cr.strmap("spec", "databases")
		names := make([]string, 0, len(dbs))
		for db := range dbs {
			names = append(names, db)
		}
		sort.Strings(names)
		if len(names) != 0 {
			f.database = literal(names[0])
			if acct, ok := f.users[dbs[names[0]]]; ok {
				f.account = acct
			}
		}
		return f, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster:
			return roleRW
		case cluster + "-repl":
			return roleRO
		}
		return ""
	},
	fromLabels: func(labels map[string]string) string {
		if labels["application"] == "spilo" {
			return labels["cluster-name"]
		}
		return ""
	},
}

// crunchy is Crunchy PGO, read at v6.0.2. Its primary Service has no
// selector.
var crunchy = &operator{
	name:     "Crunchy PGO",
	key:      "pgo",
	kind:     "PostgresCluster",
	groups:   []string{"postgres-operator.crunchydata.com"},
	resource: "postgresclusters.postgres-operator.crunchydata.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		port := cr.num("spec", "port")
		if port == 0 {
			port = 5432
		}
		service := name + "-primary"
		if pick(role) == roleRO {
			service = name + "-replicas"
		}
		f := &found{
			scheme:  "postgres",
			forward: "service/" + service,
			port:    port,
			role:    pick(role),
			pods:    "postgres-operator.crunchydata.com/cluster=" + name + ",postgres-operator.crunchydata.com/role in (" + roleLabel(role, "master,primary", "replica") + ")",
			users:   map[string]account{},
		}
		users := cr.list("spec", "users")
		for _, u := range users {
			user := u.str("name")
			f.users[user] = account{user: literal(user), password: fromSecret(name+"-pguser-"+user, "password")}
		}
		if len(users) == 0 {
			// with no users, PGO makes one with the name of the cluster, and
			// a database of the same name
			f.users[name] = account{user: literal(name), password: fromSecret(name+"-pguser-"+name, "password")}
			f.account, f.database = f.users[name], literal(name)
			return f, nil
		}
		first := users[0]
		f.account = f.users[first.str("name")]
		if dbs, ok := first.get("databases").([]any); ok && len(dbs) != 0 {
			if db, ok := dbs[0].(string); ok {
				f.database = literal(db)
			}
		}
		return f, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster + "-primary":
			return roleRW
		case cluster + "-replicas":
			return roleRO
		}
		return ""
	},
}

// stackgres is StackGres, read at 1.19.4. Its read-write Service has no
// selector, and its Secret holds the superuser.
var stackgres = &operator{
	name:     "StackGres",
	key:      "stackgres",
	kind:     "SGCluster",
	groups:   []string{"stackgres.io"},
	resource: "sgclusters.stackgres.io",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		service := name
		if pick(role) == roleRO {
			service = name + "-replicas"
		}
		su := account{user: fromSecret(name, "superuser-username"), password: fromSecret(name, "superuser-password")}
		su.user.optional, su.user.fallback = true, &credential{value: "postgres", set: true}
		return &found{
			scheme:   "postgres",
			forward:  "service/" + service,
			port:     5432,
			role:     pick(role),
			pods:     "stackgres.io/cluster-name=" + name + ",role in (" + roleLabel(role, "primary,master", "replica") + ")",
			account:  su,
			users:    map[string]account{"postgres": su},
			database: literal("postgres"),
		}, nil
	},
	roleOf: func(cluster, service string) string {
		switch service {
		case cluster, cluster + "-primary":
			return roleRW
		case cluster + "-replicas":
			return roleRO
		}
		return ""
	},
}
