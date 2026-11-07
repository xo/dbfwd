package k8s

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/xo/dbfwd"
)

const cnpgGroup = "postgresql.cnpg.io/v1"

// cnpgCluster adds a CloudNativePG cluster pg with its Services, pods and
// Secrets.
func cnpgCluster(r *fakeRunner, superuser bool) {
	r.add("clusters.postgresql.cnpg.io", map[string]any{
		"metadata": meta("pg", nil),
		"spec": map[string]any{
			"instances":             2,
			"enableSuperuserAccess": superuser,
			"managed": map[string]any{"roles": []any{
				map[string]any{"name": "reporting", "passwordSecret": map[string]any{"name": "pg-reporting"}},
			}},
		},
	})
	for _, s := range []string{"pg-rw", "pg-ro"} {
		r.add("service", map[string]any{
			"metadata": meta(s, map[string]string{"cnpg.io/cluster": "pg"}, cnpgGroup, "Cluster", "pg"),
			"spec": map[string]any{
				"selector": map[string]any{"cnpg.io/cluster": "pg"},
				"ports":    []any{map[string]any{"port": 5432}},
			},
		})
	}
	r.add("pod", map[string]any{
		"metadata": meta("pg-1", map[string]string{"cnpg.io/cluster": "pg", "cnpg.io/instanceRole": "primary"}, cnpgGroup, "Cluster", "pg"),
	})
	r.add("secret", secret("pg-app", map[string]string{"username": "app", "password": "app-pass"}))
	r.add("secret", secret("pg-superuser", map[string]string{"username": "postgres", "password": "su-pass"}))
	r.add("secret", secret("pg-reporting", map[string]string{"username": "reporting", "password": "rep-pass"}))
	r.add("secret", secret("other", map[string]string{"username": "svc", "token": "tok-pass"}))
}

// addService adds a Service with a selector, and a pod that it selects.
func addService(r *fakeRunner, name string) {
	r.add("service", map[string]any{
		"metadata": meta(name, nil),
		"spec": map[string]any{
			"selector": map[string]any{"app": name},
			"ports":    []any{map[string]any{"port": 3306}},
		},
	})
	r.add("pod", map[string]any{"metadata": meta(name+"-0", map[string]string{"app": name})})
}

// localOf parses the local URL of an Opened.
func localOf(t *testing.T, o *dbfwd.Opened) *url.URL {
	t.Helper()
	u, err := url.Parse(o.URL)
	if err != nil {
		t.Fatalf("url.Parse error = %v", err)
	}
	return u
}

func TestParseRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw  string
		want request
		bad  bool
	}{
		{raw: "k://pg-rw.prod/app", want: request{name: "pg-rw", namespace: "prod", database: "app"}},
		{raw: "k8s+cnpg://pg/", want: request{name: "pg", typ: "cnpg"}},
		{raw: "k+postgres://my.pod.dev:6432/db", want: request{name: "my.pod", namespace: "dev", typ: "postgres", port: 6432, database: "db"}},
		{raw: "k://reporting@pg.prod/", want: request{name: "pg", namespace: "prod", user: "reporting", userSet: true}},
		{raw: "k://pg-app:token@pg.prod/", want: request{name: "pg", namespace: "prod", secretForm: true, secret: "pg-app", field: "token"}},
		{raw: "k://pg-app:@pg.prod/", want: request{name: "pg", namespace: "prod", secretForm: true, secret: "pg-app", field: "password"}},
		{raw: "k://pg.prod/?context=arn:aws:eks:us-east-1:1:cluster/x&role=RO&sslmode=disable", want: request{name: "pg", namespace: "prod", context: "arn:aws:eks:us-east-1:1:cluster/x", role: "ro"}},
		{raw: "k://pg.prod/?role=primary", bad: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			got, err := parseRequest(parse(t, test.raw))
			if test.bad {
				if !errors.Is(err, dbfwd.ErrBadURL) {
					t.Fatalf("error = %v, expected %v", err, dbfwd.ErrBadURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			got.query = nil
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("request = %+v, expected %+v", got, test.want)
			}
		})
	}
	req, err := parseRequest(parse(t, "k://pg.prod/?context=c&role=rw&sslmode=disable"))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got := req.query.Encode(); got != "sslmode=disable" {
		t.Errorf("driver query = %q, expected sslmode=disable", got)
	}
}

func TestCNPGCluster(t *testing.T) {
	t.Parallel()
	r := newFake()
	cnpgCluster(r, false)
	o, err := open(t, r, "k+cnpg://pg.prod/?sslmode=disable")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	u := localOf(t, o)
	pass, _ := u.User.Password()
	if u.Scheme != "postgres" || u.User.Username() != "app" || pass != "app-pass" || u.Path != "/app" || u.Port() != "41234" {
		t.Errorf("local URL = %s, expected postgres://app:app-pass@127.0.0.1:41234/app", o.URL)
	}
	if u.Query().Get("sslmode") != "disable" || u.Query().Has("context") {
		t.Errorf("local query = %q, expected only sslmode", u.RawQuery)
	}
	if want := "--namespace prod port-forward --address 127.0.0.1 --pod-running-timeout 20s pod/pg-1 :5432"; r.forwards[0] != want {
		t.Errorf("forward = %q, expected %q", r.forwards[0], want)
	}
	want := "Cluster pg in context kind-test, namespace prod, owned by CloudNativePG Cluster pg"
	if got := o.Target.String(); got != want || o.Target.Type != "postgres" || o.Target.Backend != "k8s" {
		t.Errorf("target = %q, expected %q", got, want)
	}
}

func TestCNPGService(t *testing.T) {
	t.Parallel()
	r := newFake()
	cnpgCluster(r, false)
	o, err := open(t, r, "k://pg-ro.prod/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if !strings.Contains(r.forwards[0], "pod/pg-1 :5432") {
		t.Errorf("forward = %q, expected pod/pg-1", r.forwards[0])
	}
	if got := o.Target.String(); got != "Service pg-ro in context kind-test, namespace prod, owned by CloudNativePG Cluster pg" {
		t.Errorf("target = %q", got)
	}
	_, err = open(t, r, "k://pg-ro.prod/?role=rw")
	if !errors.Is(err, dbfwd.ErrRoleConflict) {
		t.Errorf("Open with role=rw on pg-ro error = %v, expected %v", err, dbfwd.ErrRoleConflict)
	}
}

func TestCNPGOneInstanceHasNoReplica(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("clusters.postgresql.cnpg.io", map[string]any{
		"metadata": meta("solo", nil),
		"spec":     map[string]any{"instances": 1},
	})
	if _, err := open(t, r, "k+cnpg://solo.prod/?role=ro"); !errors.Is(err, dbfwd.ErrNotFound) {
		t.Errorf("Open with role=ro error = %v, expected %v", err, dbfwd.ErrNotFound)
	}
}

// cnpgPoolers adds a Pooler of each type to the cluster pg, with the Service
// and the Deployment of the rw one.
func cnpgPoolers(r *fakeRunner) {
	for _, p := range []struct{ name, typ string }{{"pg-pooler-rw", "rw"}, {"pg-pooler-ro", "ro"}} {
		r.add("poolers.postgresql.cnpg.io", map[string]any{
			"metadata": meta(p.name, nil),
			"spec":     map[string]any{"cluster": map[string]any{"name": "pg"}, "type": p.typ},
		})
	}
	r.add("service", map[string]any{
		"metadata": meta("pg-pooler-rw", map[string]string{"cnpg.io/cluster": "pg"}, cnpgGroup, "Pooler", "pg-pooler-rw"),
		"spec": map[string]any{
			"selector": map[string]any{"cnpg.io/poolerName": "pg-pooler-rw"},
			"ports":    []any{map[string]any{"port": 5432, "targetPort": "pgbouncer"}},
		},
	})
	r.add("service", map[string]any{
		"metadata": meta("pg-pooler-ro", nil, cnpgGroup, "Pooler", "pg-pooler-ro"),
		"spec": map[string]any{
			"selector": map[string]any{"cnpg.io/poolerName": "pg-pooler-ro"},
			"ports":    []any{map[string]any{"port": 5432, "targetPort": "pgbouncer"}},
		},
	})
	r.add("pod", map[string]any{"metadata": meta("pg-pooler-ro-1", map[string]string{"cnpg.io/poolerName": "pg-pooler-ro"})})
	r.add("deployment.apps", map[string]any{
		"metadata": meta("pg-pooler-rw", nil, cnpgGroup, "Pooler", "pg-pooler-rw"),
	})
	r.add("replicaset.apps", map[string]any{
		"metadata": meta("pg-pooler-rw-7d9f", nil, "apps/v1", "Deployment", "pg-pooler-rw"),
	})
	r.add("pod", map[string]any{
		"metadata": meta("pg-pooler-rw-7d9f-abcde", map[string]string{"cnpg.io/poolerName": "pg-pooler-rw"}, "apps/v1", "ReplicaSet", "pg-pooler-rw-7d9f"),
	})
}

func TestCNPGPooler(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ raw, forward, kind string }{
		{"k://pg-pooler-rw.prod/", "pod/pg-pooler-rw-7d9f-abcde :5432", "Pooler"},
		{"k+cnpg://pg-pooler-ro.prod/", "pod/pg-pooler-ro-1 :5432", "Pooler"},
		{"k://pg-pooler-rw-7d9f-abcde.prod/", "pod/pg-pooler-rw-7d9f-abcde :5432", "Pod"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			r := newFake()
			cnpgCluster(r, false)
			cnpgPoolers(r)
			o, err := open(t, r, test.raw)
			if err != nil {
				t.Fatalf("Open error = %v", err)
			}
			if !strings.HasSuffix(r.forwards[0], test.forward) {
				t.Errorf("forward = %q, expected %s", r.forwards[0], test.forward)
			}
			u := localOf(t, o)
			pass, _ := u.User.Password()
			if u.User.Username() != "app" || pass != "app-pass" || u.Path != "/app" {
				t.Errorf("local URL = %s, expected the app account of the cluster", o.URL)
			}
			if o.Target.Kind != test.kind || o.Target.Owner != "CloudNativePG Cluster pg" {
				t.Errorf("target = %+v, expected a %s owned by the cluster pg", o.Target, test.kind)
			}
		})
	}
	r := newFake()
	cnpgCluster(r, false)
	cnpgPoolers(r)
	if _, err := open(t, r, "k://pg-pooler-ro.prod/?role=rw"); !errors.Is(err, dbfwd.ErrRoleConflict) {
		t.Errorf("Open of the ro pooler with role=rw error = %v, expected %v", err, dbfwd.ErrRoleConflict)
	}
}

func TestServiceOfAPoolerByName(t *testing.T) {
	t.Parallel()
	// with no type, the Service of a Pooler matches in step 2 by the name of
	// the Pooler, before the Service step
	r := newFake()
	cnpgCluster(r, false)
	cnpgPoolers(r)
	m, err := lookupURL(t, r, "k://pg-pooler-rw.prod/")
	if err != nil {
		t.Fatalf("Lookup error = %v", err)
	}
	if last := m.Steps[len(m.Steps)-1]; last.Kind != "CloudNativePG Pooler" || last.Result != resultMatch {
		t.Errorf("last step = %+v, expected a Pooler match", last)
	}
}

func TestDiscoveryRefused(t *testing.T) {
	t.Parallel()
	// when api-resources fails, each step asks for itself
	r := newFake()
	r.noDiscovery = true
	cnpgCluster(r, false)
	if _, err := open(t, r, "k://pg-rw.prod/"); err != nil {
		t.Fatalf("Open error = %v", err)
	}
}

func TestCNPGDatabase(t *testing.T) {
	t.Parallel()
	r := newFake()
	cnpgCluster(r, false)
	r.add("databases.postgresql.cnpg.io", map[string]any{
		"metadata": meta("billing", nil),
		"spec":     map[string]any{"cluster": map[string]any{"name": "pg"}, "name": "billing", "owner": "reporting"},
	})
	o, err := open(t, r, "k://billing.prod/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	u := localOf(t, o)
	if u.User.Username() != "reporting" || u.Path != "/billing" || !strings.Contains(r.forwards[0], "pod/pg-1") {
		t.Errorf("local URL = %s, forward = %q, expected reporting on billing through pg-rw", o.URL, r.forwards[0])
	}
}

func TestCNPGPod(t *testing.T) {
	t.Parallel()
	r := newFake()
	cnpgCluster(r, false)
	if _, err := open(t, r, "k://pg-1.prod/"); err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if !strings.Contains(r.forwards[0], "pod/pg-1 :5432") {
		t.Errorf("forward = %q, expected pod/pg-1", r.forwards[0])
	}
}

func TestCNPGUsers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw, user, pass string
		superuser       bool
		err             error
	}{
		{raw: "k://reporting@pg.prod/", user: "reporting", pass: "rep-pass"},
		{raw: "k://postgres@pg.prod/", superuser: true, user: "postgres", pass: "su-pass"},
		{raw: "k://postgres@pg.prod/", err: dbfwd.ErrNotFound},
		{raw: "k://nobody@pg.prod/", err: dbfwd.ErrNotFound},
		{raw: "k://other:token@pg.prod/", user: "svc", pass: "tok-pass"},
		{raw: "k://pg-app:@pg.prod/", user: "app", pass: "app-pass"},
		{raw: "k://other:missing@pg.prod/", err: dbfwd.ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			r := newFake()
			cnpgCluster(r, test.superuser)
			o, err := open(t, r, test.raw)
			if test.err != nil {
				if !errors.Is(err, test.err) {
					t.Fatalf("error = %v, expected %v", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			u := localOf(t, o)
			pass, _ := u.User.Password()
			if u.User.Username() != test.user || pass != test.pass {
				t.Errorf("user, password = %q, %q, expected %q, %q", u.User.Username(), pass, test.user, test.pass)
			}
		})
	}
}

func TestMOCO(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("mysqlclusters.moco.cybozu.com", map[string]any{"metadata": meta("orders", nil)})
	addService(r, "moco-orders-primary")
	addService(r, "moco-orders-replica")
	r.add("secret", secret("moco-orders", map[string]string{"WRITABLE_PASSWORD": "w-pass", "READONLY_PASSWORD": "r-pass"}))
	o, err := open(t, r, "k+moco://orders.shop/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	u := localOf(t, o)
	pass, _ := u.User.Password()
	if u.Scheme != "mysql" || u.User.Username() != "moco-writable" || pass != "w-pass" || u.Path != "" {
		t.Errorf("local URL = %s, expected mysql://moco-writable:w-pass@127.0.0.1:41234 with no database", o.URL)
	}
	if !strings.Contains(r.forwards[0], "pod/moco-orders-primary-0 :3306") {
		t.Errorf("forward = %q", r.forwards[0])
	}
	if _, err := open(t, r, "k+moco://orders.shop/?role=ro"); err != nil {
		t.Fatalf("Open with role=ro error = %v", err)
	}
	if !strings.Contains(r.forwards[1], "pod/moco-orders-replica-0") {
		t.Errorf("forward with role=ro = %q", r.forwards[1])
	}
}

func TestMariaDBOperator(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("mariadbs.k8s.mariadb.com", map[string]any{
		"metadata": meta("maria", nil),
		"spec": map[string]any{
			"username":    "shop",
			"database":    "shop",
			"replication": map[string]any{"enabled": true},
		},
	})
	r.add("secret", secret("maria-password", map[string]string{"password": "shop-pass"}))
	addService(r, "maria-primary")
	r.add("secret", secret("maria-root", map[string]string{"password": "root-pass"}))
	o, err := open(t, r, "k://maria.db/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	u := localOf(t, o)
	pass, _ := u.User.Password()
	if u.Scheme != "mariadb" || u.User.Username() != "shop" || pass != "shop-pass" || u.Path != "/shop" {
		t.Errorf("local URL = %s", o.URL)
	}
	if !strings.Contains(r.forwards[0], "pod/maria-primary-0 :3306") {
		t.Errorf("forward = %q", r.forwards[0])
	}
	o, err = open(t, r, "k://root@maria.db/")
	if err != nil {
		t.Fatalf("Open as root error = %v", err)
	}
	if pass, _ := localOf(t, o).User.Password(); pass != "root-pass" {
		t.Errorf("root password = %q, expected root-pass", pass)
	}
}

func TestOpenEverest(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("databaseclusters.everest.percona.com", map[string]any{
		"metadata": meta("shop", nil),
		"spec":     map[string]any{"engine": map[string]any{"type": "pxc"}},
		"status":   map[string]any{"hostname": "shop-haproxy.db.svc.cluster.local", "port": 3306},
	})
	r.add("databaseclusters.everest.percona.com", map[string]any{
		"metadata": meta("docs", nil),
		"spec":     map[string]any{"engine": map[string]any{"type": "psmdb"}},
	})
	r.add("secret", secret("everest-secrets-shop", map[string]string{"root": "root-pass"}))
	addService(r, "shop-haproxy")
	o, err := open(t, r, "k://shop.db/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if u := localOf(t, o); u.User.Username() != "root" || !strings.Contains(r.forwards[0], "pod/shop-haproxy-0 :3306") {
		t.Errorf("local URL = %s, forward = %q", o.URL, r.forwards[0])
	}
	if _, err := open(t, r, "k://docs.db/"); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("Open of MongoDB error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
}

// bitnamiPostgres adds a Bitnami postgresql release my-pg, which passes the
// password in a file by default.
func bitnamiPostgres(r *fakeRunner) {
	labels := map[string]string{"app.kubernetes.io/instance": "my-pg", "app.kubernetes.io/name": "postgresql"}
	svc := map[string]any{
		"metadata": meta("my-pg-postgresql", labels),
		"spec": map[string]any{
			"selector": map[string]any{"app.kubernetes.io/instance": "my-pg"},
			"ports":    []any{map[string]any{"port": 5432, "targetPort": "tcp-postgresql"}},
		},
	}
	headless := map[string]any{
		"metadata": meta("my-pg-postgresql-hl", labels),
		"spec": map[string]any{
			"clusterIP": "None",
			"ports":     []any{map[string]any{"port": 5432}},
		},
	}
	pod := map[string]any{
		"metadata": meta("my-pg-postgresql-0", labels, "apps/v1", "StatefulSet", "my-pg-postgresql"),
		"status":   map[string]any{"phase": "Running"},
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name":  "postgresql",
					"image": "docker.io/bitnamilegacy/postgresql:17.6.0-debian-12-r0",
					"ports": []any{map[string]any{"name": "tcp-postgresql", "containerPort": 5432}},
					"env": []any{
						map[string]any{"name": "POSTGRES_PASSWORD_FILE", "value": "/opt/bitnami/postgresql/secrets/postgres-password"},
					},
					"volumeMounts": []any{map[string]any{"name": "postgresql-password", "mountPath": "/opt/bitnami/postgresql/secrets/"}},
				},
				map[string]any{
					"name":  "metrics",
					"image": "docker.io/bitnami/postgres-exporter:0.17",
					"env":   []any{map[string]any{"name": "DATA_SOURCE_PASS", "value": "nope"}},
				},
			},
			"volumes": []any{map[string]any{"name": "postgresql-password", "secret": map[string]any{"secretName": "my-pg-postgresql"}}},
		},
	}
	r.add("service", svc)
	r.add("service", headless)
	r.add("statefulset.apps", map[string]any{"metadata": meta("my-pg-postgresql", labels)})
	r.add("pod", pod)
	r.addList("services", "app.kubernetes.io/instance=my-pg", svc, headless)
	r.addList("pods", "app.kubernetes.io/instance=my-pg", pod)
	r.add("secret", secret("my-pg-postgresql", map[string]string{"postgres-password": "pg-pass"}))
}

func TestBitnamiRelease(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"k://my-pg.dev/", "k://my-pg-postgresql.dev/", "k://my-pg-postgresql-0.dev/"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			r := newFake()
			bitnamiPostgres(r)
			o, err := open(t, r, raw)
			if err != nil {
				t.Fatalf("Open error = %v", err)
			}
			u := localOf(t, o)
			pass, _ := u.User.Password()
			if u.Scheme != "postgres" || u.User.Username() != "postgres" || pass != "pg-pass" || u.Path != "/postgres" {
				t.Errorf("local URL = %s, expected postgres://postgres:pg-pass@.../postgres", o.URL)
			}
			if o.Target.Owner != "" || o.Target.Type != "postgres" {
				t.Errorf("target = %+v, expected no owner and the type postgres", o.Target)
			}
		})
	}
}

func TestPlainPod(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("pod", map[string]any{
		"metadata": meta("shop-db", nil),
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name":  "exporter",
					"image": "prom/mysqld-exporter",
					"env":   []any{map[string]any{"name": "MYSQL_ROOT_PASSWORD", "value": "not-this"}},
				},
				map[string]any{
					"name":  "db",
					"image": "registry.local/mirror/mysql:8.4",
					"ports": []any{map[string]any{"containerPort": 3306}},
					"env": []any{
						map[string]any{"name": "MYSQL_USER", "value": "shop"},
						map[string]any{"name": "MYSQL_DATABASE", "value": "shop"},
						map[string]any{"name": "MYSQL_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "shop-db", "key": "pw"}}},
					},
				},
			},
		},
	})
	r.add("secret", secret("shop-db", map[string]string{"pw": "shop-pass"}))
	o, err := open(t, r, "k://shop-db.dev/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	u := localOf(t, o)
	pass, _ := u.User.Password()
	if u.Scheme != "mysql" || u.User.Username() != "shop" || pass != "shop-pass" || u.Path != "/shop" {
		t.Errorf("local URL = %s", o.URL)
	}
}

func TestEnvFrom(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("pod", map[string]any{
		"metadata": meta("pg-0", nil),
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name":    "postgres",
				"image":   "postgres:18",
				"env":     []any{map[string]any{"name": "POSTGRES_USER", "value": "app"}},
				"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "pg-env"}}},
			}},
		},
	})
	r.add("secret", secret("pg-env", map[string]string{"POSTGRES_PASSWORD": "env-pass"}))
	o, err := open(t, r, "k://pg-0.dev/")
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if pass, _ := localOf(t, o).User.Password(); pass != "env-pass" {
		t.Errorf("password = %q, expected env-pass", pass)
	}
}

func TestMongoDBIsUnsupported(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("pod", map[string]any{
		"metadata": meta("docs-0", nil),
		"spec": map[string]any{"containers": []any{map[string]any{
			"name": "mongo", "image": "mongo:8",
			"env": []any{map[string]any{"name": "MONGO_INITDB_ROOT_USERNAME", "value": "admin"}},
		}}},
	})
	if _, err := open(t, r, "k://docs-0.dev/"); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("Open error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
}

func TestExternalNameIsUnsupported(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.add("service", map[string]any{
		"metadata": meta("rds", nil),
		"spec":     map[string]any{"type": "ExternalName", "externalName": "db.example.com"},
	})
	if _, err := open(t, r, "k://rds.dev/"); !errors.Is(err, dbfwd.ErrUnsupported) {
		t.Errorf("Open error = %v, expected %v", err, dbfwd.ErrUnsupported)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.installed["clusters.postgresql.cnpg.io"] = true
	_, err := open(t, r, "k://nothing.dev/")
	if !errors.Is(err, dbfwd.ErrNotFound) {
		t.Fatalf("Open error = %v, expected %v", err, dbfwd.ErrNotFound)
	}
	var le *dbfwd.LookupError
	if !errors.As(err, &le) {
		t.Fatalf("error = %T, expected a LookupError", err)
	}
	var got []string
	for _, s := range le.Steps {
		got = append(got, s.Kind+": "+s.Result)
	}
	var want []string
	for _, op := range operators {
		result := "not installed"
		if op == cnpg {
			result = "no match"
		}
		want = append(want, op.name+" "+op.kind+": "+result)
	}
	for _, op := range operators {
		for _, c := range op.children {
			want = append(want, op.name+" "+c.kind+": not installed")
		}
	}
	want = append(want, "Service: no match", "Helm release: no match", "StatefulSet: no match", "Deployment: no match", "Pod: no match")
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("steps =\n%s\nexpected\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestForbidden(t *testing.T) {
	t.Parallel()
	r := newFake()
	r.forbidden["service"] = true
	if _, err := open(t, r, "k://nothing.dev/"); !errors.Is(err, dbfwd.ErrForbidden) {
		t.Errorf("Open error = %v, expected %v", err, dbfwd.ErrForbidden)
	}
	r = newFake()
	cnpgCluster(r, false)
	r.forbidden["secret/pg-app"] = true
	if _, err := open(t, r, "k://pg.prod/"); !errors.Is(err, dbfwd.ErrForbidden) {
		t.Errorf("Open with no access to the secret error = %v, expected %v", err, dbfwd.ErrForbidden)
	}
}

func TestLookupReadsNoSecret(t *testing.T) {
	t.Parallel()
	r := newFake()
	bitnamiPostgres(r)
	m, err := lookupURL(t, r, "k://my-pg.dev/")
	if err != nil {
		t.Fatalf("Lookup error = %v", err)
	}
	if r.readSecret() {
		t.Errorf("Lookup read a secret: %v", r.calls)
	}
	if m.Target.Kind != "Service" || m.Target.Name != "my-pg-postgresql" {
		t.Errorf("target = %+v, expected the Service my-pg-postgresql", m.Target)
	}
	if last := m.Steps[len(m.Steps)-1]; last.Kind != "Helm release" || last.Result != resultMatch {
		t.Errorf("last step = %+v, expected a Helm release match", last)
	}
}

func TestNoErrorHoldsAPassword(t *testing.T) {
	t.Parallel()
	r := newFake()
	cnpgCluster(r, false)
	for _, raw := range []string{"k://other:missing@pg.prod/", "k://nobody@pg.prod/", "k://pg-ro.prod/?role=rw"} {
		_, err := open(t, r, raw)
		if err == nil {
			t.Fatalf("Open(%s) error = nil, expected an error", raw)
		}
		for _, p := range []string{"app-pass", "su-pass", "rep-pass", "tok-pass"} {
			if strings.Contains(err.Error(), p) {
				t.Errorf("Open(%s) error holds a password: %v", raw, err)
			}
		}
	}
}
