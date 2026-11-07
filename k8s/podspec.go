package k8s

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/xo/dbfwd"
)

// dbType holds the pod spec rules of one database type, for a database that
// no operator manages. docs/LOOKUP.md holds the table.
type dbType struct {
	// name is the type, such as postgres, and scheme is the dburl scheme.
	name   string
	scheme string

	// prefixes are the prefixes of its environment variables.
	prefixes []string

	// images are the base names of its images.
	images []string

	// port is its default port.
	port int

	// supported is false for a type that no database/sql driver reaches.
	supported bool

	// accounts reads the default account, the other users and the database
	// from the environment.
	accounts func(e env) (account, map[string]account, credential)
}

// dbTypes is every type that the pod spec rules cover.
var dbTypes = []*dbType{
	{
		name: "postgres", scheme: "postgres", port: 5432, supported: true,
		prefixes: []string{"POSTGRES_"},
		images:   []string{"postgres", "postgresql"},
		accounts: func(e env) (account, map[string]account, credential) {
			user := e.first("POSTGRES_USER")
			if !user.known() {
				user = literal("postgres")
			}
			def := account{user: user, password: e.first("POSTGRES_PASSWORD")}
			users := map[string]account{}
			if u := user.show(); user.secret == "" {
				users[u] = def
			}
			if p := e.first("POSTGRES_POSTGRES_PASSWORD"); p.known() {
				users["postgres"] = account{user: literal("postgres"), password: p}
			}
			db := e.first("POSTGRES_DB", "POSTGRES_DATABASE")
			switch {
			case !db.known():
				db = user
			case db.optional:
				db.fallback = &user
			}
			return def, users, db
		},
	},
	{
		name: "mariadb", scheme: "mariadb", port: 3306, supported: true,
		prefixes: []string{"MARIADB_"},
		images:   []string{"mariadb"},
		accounts: mysqlAccounts("MARIADB_", "MYSQL_"),
	},
	{
		name: "mysql", scheme: "mysql", port: 3306, supported: true,
		prefixes: []string{"MYSQL_"},
		images:   []string{"mysql"},
		accounts: mysqlAccounts("MYSQL_"),
	},
	{
		name: "sqlserver", scheme: "sqlserver", port: 1433, supported: true,
		prefixes: []string{"MSSQL_", "SA_PASSWORD"},
		images:   []string{"mssql/server", "server"},
		accounts: func(e env) (account, map[string]account, credential) {
			sa := account{user: literal("sa"), password: e.first("MSSQL_SA_PASSWORD", "SA_PASSWORD")}
			return sa, map[string]account{"sa": sa}, literal("master")
		},
	},
	{
		name: "clickhouse", scheme: "clickhouse", port: 9000, supported: true,
		prefixes: []string{"CLICKHOUSE_"},
		images:   []string{"clickhouse", "clickhouse-server"},
		accounts: func(e env) (account, map[string]account, credential) {
			user := e.first("CLICKHOUSE_ADMIN_USER", "CLICKHOUSE_USER")
			if !user.known() {
				user = literal("default")
			}
			def := account{user: user, password: e.first("CLICKHOUSE_ADMIN_PASSWORD", "CLICKHOUSE_PASSWORD")}
			users := map[string]account{}
			if user.secret == "" {
				users[user.show()] = def
			}
			return def, users, literal("default")
		},
	},
	{
		name: "mongodb", port: 27017,
		prefixes: []string{"MONGO_INITDB_", "MONGODB_"},
		images:   []string{"mongo", "mongodb"},
	},
	{
		name: "redis", port: 6379,
		prefixes: []string{"REDIS_"},
		images:   []string{"redis", "valkey"},
	},
}

// mysqlAccounts reads the accounts of the mysql and the mariadb images. The
// mariadb image also reads the MYSQL_ names.
func mysqlAccounts(prefixes ...string) func(env) (account, map[string]account, credential) {
	names := func(suffix string) []string {
		out := make([]string, 0, len(prefixes))
		for _, p := range prefixes {
			out = append(out, p+suffix)
		}
		return out
	}
	return func(e env) (account, map[string]account, credential) {
		root := account{user: literal("root"), password: e.first(names("ROOT_PASSWORD")...)}
		users := map[string]account{"root": root}
		def := root
		if user := e.first(names("USER")...); user.known() {
			def = account{user: user, password: e.first(names("PASSWORD")...)}
			if user.secret == "" {
				users[user.show()] = def
			}
		}
		return def, users, e.first(names("DATABASE")...)
	}
}

// typeNamed returns the type with the name, or nil.
func typeNamed(name string) *dbType {
	for _, t := range dbTypes {
		if t.name == name {
			return t
		}
	}
	return nil
}

// env is the environment of one container, with where each value comes from.
type env struct {
	vars map[string]credential

	// from holds the Secrets that envFrom loads whole.
	from []string
}

// first returns the first of the names that the environment sets. A name
// that only envFrom can set comes from the first such Secret.
func (e env) first(names ...string) credential {
	for _, n := range names {
		if c, ok := e.vars[n]; ok {
			return c
		}
		if c, ok := e.vars[n+"_FILE"]; ok {
			return c
		}
	}
	if len(e.from) != 0 && len(names) != 0 {
		c := fromSecret(e.from[0], names[0])
		c.optional = true
		return c
	}
	return credential{}
}

// readEnv reads the environment of a container in a pod spec.
func readEnv(spec, container object) env {
	e := env{vars: map[string]credential{}}
	for _, v := range container.list("env") {
		name := v.str("name")
		switch {
		case v.get("valueFrom", "secretKeyRef") != nil:
			e.vars[name] = fromSecret(v.str("valueFrom", "secretKeyRef", "name"), v.str("valueFrom", "secretKeyRef", "key"))
		case strings.HasSuffix(name, "_FILE"):
			if c, ok := fileSecret(spec, container, v.str("value")); ok {
				e.vars[name] = c
			}
		case v.get("value") != nil:
			e.vars[name] = literal(v.str("value"))
		}
	}
	for _, f := range container.list("envFrom") {
		if s := f.str("secretRef", "name"); s != "" {
			e.from = append(e.from, s)
		}
	}
	return e
}

// fileSecret follows a path in a container to the Secret volume mounted
// there. The base name of the file is the key, unless the volume maps it.
func fileSecret(spec, container object, file string) (credential, bool) {
	for _, m := range container.list("volumeMounts") {
		dir := strings.TrimSuffix(m.str("mountPath"), "/")
		if dir == "" || !strings.HasPrefix(file, dir+"/") {
			continue
		}
		rel := strings.TrimPrefix(file, dir+"/")
		for _, v := range spec.list("volumes") {
			if v.str("name") != m.str("name") || v.get("secret") == nil {
				continue
			}
			key := path.Base(rel)
			for _, item := range v.list("secret", "items") {
				if item.str("path") == rel {
					key = item.str("key")
				}
			}
			return fromSecret(v.str("secret", "secretName"), key), true
		}
	}
	return credential{}, false
}

// notDatabase reports whether an image is a helper and never the database.
func notDatabase(image string) bool {
	base := imageBase(image)
	for _, w := range []string{"exporter", "pgbouncer", "proxy"} {
		if strings.Contains(base, w) {
			return true
		}
	}
	return false
}

// imageBase returns the repository of an image with no registry and no tag,
// such as postgres for docker.io/library/postgres:18.
func imageBase(image string) string {
	if i := strings.IndexByte(image, '@'); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndexByte(image, ':'); i > strings.LastIndexByte(image, '/') {
		image = image[:i]
	}
	if strings.HasSuffix(image, "mssql/server") {
		return "mssql/server"
	}
	return path.Base(image)
}

// imageType returns the type whose image the container runs, or nil.
func imageType(image string) *dbType {
	base := imageBase(image)
	for _, t := range dbTypes {
		if slices.Contains(t.images, base) {
			return t
		}
	}
	return nil
}

// containerPort returns the port of a container with the name, or zero.
func containerPort(c object, name string) int {
	for _, p := range c.list("ports") {
		if p.str("name") == name {
			return p.num("containerPort")
		}
	}
	return 0
}

// hasPort reports whether a container listens on port.
func hasPort(c object, port int) bool {
	for _, p := range c.list("ports") {
		if p.num("containerPort") == port {
			return true
		}
	}
	return false
}

// pickContainer returns the database container of a pod spec. target is the
// port that a Service targets, as a number or a name, or empty.
func pickContainer(spec object, target string) (object, bool) {
	var candidates []object
	for _, c := range spec.list("containers") {
		if !notDatabase(c.str("image")) {
			candidates = append(candidates, c)
		}
	}
	for _, c := range candidates {
		if imageType(c.str("image")) != nil {
			return c, true
		}
	}
	if target != "" {
		n, err := strconv.Atoi(target)
		for _, c := range candidates {
			if (err == nil && hasPort(c, n)) || (err != nil && containerPort(c, target) != 0) {
				return c, true
			}
		}
	}
	for _, c := range candidates {
		for _, t := range dbTypes {
			if hasPort(c, t.port) {
				return c, true
			}
		}
	}
	return nil, false
}

// detectType finds the type of the database container from its environment,
// its image, its ports and the chart label, in that order of trust. It stops
// when two signals disagree.
func detectType(c object, e env, chart string) (*dbType, error) {
	byEnv, err := envType(e)
	if err != nil {
		return nil, err
	}
	byImage := imageType(c.str("image"))
	var byPort *dbType
	for _, t := range dbTypes {
		if hasPort(c, t.port) {
			byPort = t
			break
		}
	}
	byChart := typeNamed(chart)
	if chart == "postgresql" {
		byChart = typeNamed("postgres")
	}
	var picked *dbType
	for _, t := range []*dbType{byEnv, byImage, byChart} {
		switch {
		case t == nil:
		case picked == nil:
			picked = t
		case !compatible(picked, t):
			return nil, fmt.Errorf("telling the type of the database: %s and %s both fit: %w", picked.name, t.name, dbfwd.ErrAmbiguous)
		}
	}
	if picked == nil {
		picked = byPort
	}
	if picked == nil {
		return nil, fmt.Errorf("telling the type of the database: %w", dbfwd.ErrNotFound)
	}
	return picked, nil
}

// envType returns the type whose variables the environment sets, or nil.
func envType(e env) (*dbType, error) {
	var matched []*dbType
	for _, t := range dbTypes {
		for name := range e.vars {
			if hasAnyPrefix(name, t.prefixes) {
				matched = append(matched, t)
				break
			}
		}
	}
	var picked *dbType
	for _, t := range matched {
		switch {
		case picked == nil, picked.name == "mysql" && t.name == "mariadb":
			picked = t
		case !compatible(picked, t):
			return nil, fmt.Errorf("telling the type of the database: the environment fits %s and %s: %w", picked.name, t.name, dbfwd.ErrAmbiguous)
		}
	}
	return picked, nil
}

// hasAnyPrefix reports whether s starts with one of prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// compatible reports whether two signals agree. A mariadb image also reads
// the MYSQL_ names, so mysql and mariadb agree.
func compatible(a, b *dbType) bool {
	if a == b {
		return true
	}
	pair := a.name + "/" + b.name
	return pair == "mariadb/mysql" || pair == "mysql/mariadb"
}

// fromPodSpec builds what a lookup found from a pod spec, for a database that
// no operator manages. typ is the type from the URL, or empty.
func fromPodSpec(spec object, target, chart, typ string) (*found, error) {
	c, ok := pickContainer(spec, target)
	if !ok {
		return nil, fmt.Errorf("finding the database container: %w", dbfwd.ErrNotFound)
	}
	e := readEnv(spec, c)
	t := typeNamed(typ)
	if t == nil {
		var err error
		if t, err = detectType(c, e, chart); err != nil {
			return nil, err
		}
	}
	if !t.supported {
		return nil, fmt.Errorf("reaching %s, which no database/sql driver reaches: %w", t.name, dbfwd.ErrUnsupported)
	}
	def, users, db := t.accounts(e)
	port := t.port
	if n, err := strconv.Atoi(target); err == nil && n != 0 {
		port = n
	} else if n := containerPort(c, target); n != 0 {
		port = n
	} else if ports := c.list("ports"); len(ports) == 1 {
		port = ports[0].num("containerPort")
	}
	return &found{
		scheme:   t.scheme,
		port:     port,
		account:  def,
		users:    users,
		database: db,
		target:   dbfwd.Target{Type: t.name},
	}, nil
}
