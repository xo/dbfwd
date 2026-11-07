package k8s

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/xo/dbfwd"
)

// owned returns metadata with a name, labels and a controller.
func ownedMeta(name string, labels map[string]string, apiVersion, kind, owner string) map[string]any {
	m := map[string]any{"name": name}
	if labels != nil {
		l := map[string]any{}
		for k, v := range labels {
			l[k] = v
		}
		m["labels"] = l
	}
	if kind != "" {
		m["ownerReferences"] = []any{map[string]any{"apiVersion": apiVersion, "kind": kind, "name": owner, "controller": true}}
	}
	return m
}

// readyPod returns a pod that runs and is ready.
func readyPodObject(meta map[string]any, spec map[string]any) map[string]any {
	return map[string]any{
		"metadata": meta,
		"spec":     spec,
		"status": map[string]any{
			"phase":      "Running",
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}
}

// postgresSpec is the pod spec of the official postgres image, with its
// password in a Secret.
func postgresSpec(secret string) map[string]any {
	return map[string]any{"containers": []any{map[string]any{
		"name":  "postgres",
		"image": "postgres:18",
		"ports": []any{map[string]any{"containerPort": 5432}},
		"env": []any{map[string]any{
			"name":      "POSTGRES_PASSWORD",
			"valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": secret, "key": "password"}},
		}},
	}}}
}

// listFixture fills a fake with a namespace prod that holds a CloudNativePG
// cluster with a Pooler and a Database, a StatefulSet of the official postgres
// image, a web server and a CloudNativePG cluster and a MariaDB of the same
// name. It adds a second namespace, dev, with one more cluster.
func listFixture() *fakeRunner {
	r := newFake()
	const cnpgAPI = "postgresql.cnpg.io/v1"
	r.addListed("clusters.postgresql.cnpg.io", "Cluster", cnpgAPI, "prod", map[string]any{
		"metadata": map[string]any{"name": "pg"},
		"spec":     map[string]any{"instances": 3},
	})
	r.addListed("clusters.postgresql.cnpg.io", "Cluster", cnpgAPI, "prod", map[string]any{"metadata": map[string]any{"name": "shop"}, "spec": map[string]any{"instances": 1}})
	r.addListed("clusters.postgresql.cnpg.io", "Cluster", cnpgAPI, "dev", map[string]any{"metadata": map[string]any{"name": "scratch"}, "spec": map[string]any{"instances": 1}})
	r.addListed("mariadbs.k8s.mariadb.com", "MariaDB", "k8s.mariadb.com/v1alpha1", "prod", map[string]any{"metadata": map[string]any{"name": "shop"}})
	for _, svc := range []string{"pg-rw", "pg-ro", "pg-r"} {
		r.addListed("services", "Service", "v1", "prod", map[string]any{
			"metadata": ownedMeta(svc, nil, cnpgAPI, "Cluster", "pg"),
			"spec":     map[string]any{"selector": map[string]any{"cnpg.io/cluster": "pg"}},
		})
	}
	for i, role := range []string{"primary", "replica", "replica"} {
		name := "pg-" + string(rune('1'+i))
		r.addListed("pods", "Pod", "v1", "prod", readyPodObject(
			ownedMeta(name, map[string]string{"cnpg.io/cluster": "pg", "cnpg.io/instanceRole": role}, cnpgAPI, "Cluster", "pg"),
			map[string]any{"containers": []any{map[string]any{"name": "postgres", "image": "ghcr.io/cloudnative-pg/postgresql:18"}}},
		))
	}
	r.addListed("poolers.postgresql.cnpg.io", "Pooler", cnpgAPI, "prod", map[string]any{
		"metadata": map[string]any{"name": "pg-pooler-rw"},
		"spec":     map[string]any{"cluster": map[string]any{"name": "pg"}, "type": "rw"},
	})
	r.addListed("deployments.apps", "Deployment", "apps/v1", "prod", map[string]any{"metadata": ownedMeta("pg-pooler-rw", nil, cnpgAPI, "Pooler", "pg-pooler-rw")})
	r.addListed("replicasets.apps", "ReplicaSet", "apps/v1", "prod", map[string]any{"metadata": ownedMeta("pg-pooler-rw-7f", nil, "apps/v1", "Deployment", "pg-pooler-rw")})
	r.addListed("pods", "Pod", "v1", "prod", readyPodObject(ownedMeta("pg-pooler-rw-7f-x", nil, "apps/v1", "ReplicaSet", "pg-pooler-rw-7f"), map[string]any{}))
	r.addListed("databases.postgresql.cnpg.io", "Database", cnpgAPI, "prod", map[string]any{
		"metadata": map[string]any{"name": "billing"},
		"spec":     map[string]any{"cluster": map[string]any{"name": "pg"}, "name": "billing", "owner": "app"},
	})
	r.addListed("statefulsets.apps", "StatefulSet", "apps/v1", "prod", map[string]any{
		"metadata": ownedMeta("pgsts", map[string]string{"app.kubernetes.io/managed-by": "Helm", "app.kubernetes.io/instance": "shop"}, "", "", ""),
		"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "pgsts"}},
			"spec":     postgresSpec("pgsts"),
		}},
	})
	r.addListed("pods", "Pod", "v1", "prod", readyPodObject(ownedMeta("pgsts-0", map[string]string{"app": "pgsts"}, "apps/v1", "StatefulSet", "pgsts"), postgresSpec("pgsts")))
	r.addListed("services", "Service", "v1", "prod", map[string]any{
		"metadata": map[string]any{"name": "pgsts"},
		"spec":     map[string]any{"selector": map[string]any{"app": "pgsts"}},
	})
	r.addListed("deployments.apps", "Deployment", "apps/v1", "prod", map[string]any{
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{
			"name": "nginx", "image": "nginx:1.29", "ports": []any{map[string]any{"containerPort": 80}},
		}}}}},
	})
	return r
}

// listWith runs a listing of raw against r.
func listWith(t *testing.T, r *fakeRunner, raw string) (*dbfwd.Listing, error) {
	t.Helper()
	return backend{newRunner: func(dbfwd.Config) runner { return r }}.List(t.Context(), parse(t, raw), dbfwd.Config{})
}

// summary is the part of an entry that a test compares.
type summary struct {
	Namespace, Name, Kind, Type, Operator, Owner, URL string
	Roles                                             []string
	Ready, Pods                                       int
	Databases                                         []string
	Credential                                        string
}

// summarize returns the summary of each entry.
func summarize(l *dbfwd.Listing) []summary {
	var out []summary
	for _, e := range l.Entries {
		out = append(out, summary{
			Namespace: e.Target.Scope[1].Value, Name: e.Target.Name, Kind: e.Target.Kind, Type: e.Target.Type,
			Operator: e.Operator, Owner: e.Target.Owner, URL: e.URL, Roles: e.Roles,
			Ready: e.Ready, Pods: e.Pods, Databases: e.Databases, Credential: e.Credential,
		})
	}
	return out
}

func TestList(t *testing.T) {
	t.Parallel()
	r := listFixture()
	l, err := listWith(t, r, "k://*.prod")
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	want := []summary{
		{"prod", "billing", "Database", "postgres", "CloudNativePG", "CloudNativePG Cluster pg", "k://billing.prod/", []string{"rw"}, 3, 3, []string{"billing"}, "Secret pg-app key password"},
		{"prod", "pg", "Cluster", "postgres", "CloudNativePG", "CloudNativePG Cluster pg", "k://pg.prod/", []string{"rw", "ro"}, 3, 3, []string{"app"}, "Secret pg-app key password"},
		{"prod", "pg-pooler-rw", "Pooler", "postgres", "CloudNativePG", "CloudNativePG Cluster pg", "k://pg-pooler-rw.prod/", []string{"rw"}, 1, 1, []string{"app"}, "Secret pg-app key password"},
		{"prod", "pgsts", "StatefulSet", "postgres", "", "Helm release shop", "k://pgsts.prod/", []string{"rw"}, 1, 1, []string{"postgres"}, "Secret pgsts key password"},
		{"prod", "shop", "Cluster", "postgres", "CloudNativePG", "CloudNativePG Cluster shop", "k://shop.prod/", []string{"rw"}, 0, 0, []string{"app"}, "Secret shop-app key password"},
		{"prod", "shop", "MariaDB", "mariadb", "mariadb-operator", "mariadb-operator MariaDB shop", "k+mariadb-operator://shop.prod/", []string{"rw"}, 0, 0, nil, "Secret shop-root key password"},
	}
	if got := summarize(l); !reflect.DeepEqual(got, want) {
		t.Errorf("entries =\n%+v\nexpected\n%+v", got, want)
	}
	if want := []dbfwd.Field{{Name: "context", Value: "kind-test"}, {Name: "namespace", Value: "prod"}}; !reflect.DeepEqual(l.Scope, want) {
		t.Errorf("Scope = %v, expected %v", l.Scope, want)
	}
	if len(l.Skipped) != 0 {
		t.Errorf("Skipped = %v, expected none", l.Skipped)
	}
	if r.readSecret() {
		t.Errorf("a listing read a Secret: %v", r.calls)
	}
	// the kubeconfig, the kinds of the cluster, and one get of every kind
	if len(r.calls) != 3 {
		t.Errorf("calls = %d, expected 3: %v", len(r.calls), r.calls)
	}
}

func TestListFilters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw  string
		want []string
	}{
		{"k://pg*.prod/", []string{"pg", "pg-pooler-rw", "pgsts"}},
		{"k+cnpg://*.prod", []string{"billing", "pg", "pg-pooler-rw", "shop"}},
		{"k+pg://*.prod", []string{"billing", "pg", "pg-pooler-rw", "pgsts", "shop"}},
		{"k+mariadb://*.prod", []string{"shop"}},
		{"k://*.*", []string{"scratch", "billing", "pg", "pg-pooler-rw", "pgsts", "shop", "shop"}},
		{"k://*", nil},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			l, err := listWith(t, listFixture(), test.raw)
			if err != nil {
				t.Fatalf("List error = %v", err)
			}
			var got []string
			for _, e := range l.Entries {
				got = append(got, e.Target.Name)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("names = %v, expected %v", got, test.want)
			}
		})
	}
}

func TestListEveryNamespace(t *testing.T) {
	t.Parallel()
	r := listFixture()
	l, err := listWith(t, r, "k://*.*/?context=kind-test")
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if got := l.Scope[1].Value; got != "*" {
		t.Errorf("Scope namespace = %q, expected *", got)
	}
	if got := l.Entries[0].URL; got != "k://scratch.dev/?context=kind-test" {
		t.Errorf("URL = %q, expected the context that the listing named", got)
	}
	if last := r.calls[len(r.calls)-1]; !strings.Contains(last, "--all-namespaces") || strings.Contains(last, "--namespace") {
		t.Errorf("call = %q, expected --all-namespaces and no --namespace", last)
	}
}

func TestListForbidden(t *testing.T) {
	t.Parallel()
	r := listFixture()
	r.forbidden["pods"] = true
	l, err := listWith(t, r, "k://*.prod")
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(l.Skipped) != 1 || l.Skipped[0].Kind != "pods" || l.Skipped[0].Reason != "not allowed" {
		t.Errorf("Skipped = %+v, expected pods not allowed", l.Skipped)
	}
	if len(l.Entries) == 0 {
		t.Errorf("Entries is empty, expected the kinds that are allowed")
	}

	r = listFixture()
	r.forbidden["--all-namespaces"] = true
	if _, err := listWith(t, r, "k://*.*"); !errors.Is(err, dbfwd.ErrForbidden) || !strings.Contains(err.Error(), "every namespace") {
		t.Errorf("error = %v, expected %v for every namespace", err, dbfwd.ErrForbidden)
	}
}

func TestParseScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw  string
		want scope
		bad  bool
	}{
		{raw: "k://*", want: scope{pattern: "*"}},
		{raw: "k://*/", want: scope{pattern: "*"}},
		{raw: "k://*.prod", want: scope{pattern: "*", namespace: "prod"}},
		{raw: "k://*.*", want: scope{pattern: "*", namespace: "*"}},
		{raw: "k://pg*.prod/", want: scope{pattern: "pg*", namespace: "prod"}},
		{raw: "k+pg://*.prod", want: scope{pattern: "*", namespace: "prod", typ: "postgres"}},
		{raw: "k+cnpg://*?context=kind-x", want: scope{pattern: "*", typ: "cnpg", context: "kind-x"}},
		{raw: "k://*.prod/app", bad: true},
		{raw: "k://app@*.prod", bad: true},
		{raw: "k://*.prod:5432", bad: true},
		{raw: "k://*.prod?role=ro", bad: true},
		{raw: "k://*.pr*", bad: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			got, err := parseScope(parse(t, test.raw))
			if test.bad {
				if !errors.Is(err, dbfwd.ErrBadURL) {
					t.Fatalf("error = %v, expected %v", err, dbfwd.ErrBadURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != test.want {
				t.Errorf("scope = %+v, expected %+v", got, test.want)
			}
		})
	}
}

func TestListURL(t *testing.T) {
	t.Parallel()
	ns := func(v string) dbfwd.Field { return dbfwd.Field{Name: "namespace", Value: v} }
	kc := func(v string) dbfwd.Field { return dbfwd.Field{Name: "context", Value: v} }
	tests := []struct {
		scope string
		where []dbfwd.Field
		want  string
		bad   bool
	}{
		{scope: "", want: "k://*"},
		{scope: "*", want: "k://*"},
		{scope: "pg*.prod", want: "k://pg*.prod"},
		{scope: "cnpg://*", want: "k+cnpg://*"},
		{scope: "pg://*.prod", want: "k+pg://*.prod"},
		{scope: "k+cnpg://*", want: "k+cnpg://*"},
		{scope: "kube://pg.prod/", want: "kube://pg.prod/"},
		{scope: "", where: []dbfwd.Field{ns("*")}, want: "k://*.*"},
		{scope: "pg*", where: []dbfwd.Field{ns("prod")}, want: "k://pg*.prod"},
		{scope: "*.prod", where: []dbfwd.Field{ns("prod")}, want: "k://*.prod"},
		{scope: "", where: []dbfwd.Field{kc("kind-x")}, want: "k://*?context=kind-x"},
		{scope: "cnpg://*", where: []dbfwd.Field{ns("prod"), kc("kind-x")}, want: "k+cnpg://*.prod?context=kind-x"},
		{scope: "*.prod", where: []dbfwd.Field{ns("dev")}, bad: true},
		{scope: "*.prod", where: []dbfwd.Field{ns("*")}, bad: true},
		{scope: "*?context=a", where: []dbfwd.Field{kc("b")}, bad: true},
		{scope: "*", where: []dbfwd.Field{{Name: "role", Value: "ro"}}, bad: true},
	}
	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			t.Parallel()
			got, err := backend{}.ListURL(test.scope, test.where)
			if test.bad {
				if !errors.Is(err, dbfwd.ErrBadURL) {
					t.Fatalf("error = %v, expected %v", err, dbfwd.ErrBadURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != test.want {
				t.Errorf("ListURL = %q, expected %q", got, test.want)
			}
		})
	}
}
