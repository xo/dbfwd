package test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/xo/dbfwd"
)

// cnpgRelease is the release of CloudNativePG that docs/OPERATORS.md was read
// from.
const cnpgRelease = "1.30.1"

// installCNPG installs CloudNativePG from its published manifest.
func installCNPG(t *testing.T) error {
	t.Helper()
	url := "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v" + cnpgRelease + "/cnpg-" + cnpgRelease + ".yaml"
	if _, err := c.run("apply", "--server-side", "--force-conflicts", "--filename", url); err != nil {
		return err
	}
	_, err := c.run("--namespace", "cnpg-system", "rollout", "status", "deployment/cnpg-controller-manager", "--timeout=5m")
	return err
}

const cnpgFixture = `
apiVersion: v1
kind: Secret
type: kubernetes.io/basic-auth
metadata: {name: pg-reporting}
stringData: {username: reporting, password: reporting-test-only}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: pg}
spec:
  instances: 2
  enableSuperuserAccess: true
  storage: {size: 1Gi}
  managed:
    roles:
    - name: reporting
      ensure: present
      login: true
      passwordSecret: {name: pg-reporting}
---
apiVersion: postgresql.cnpg.io/v1
kind: Pooler
metadata: {name: pg-pooler-rw}
spec:
  cluster: {name: pg}
  instances: 1
  type: rw
  pgbouncer: {poolMode: session}
---
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata: {name: billing}
spec:
  cluster: {name: pg}
  name: billing
  owner: reporting
`

// setupCNPG installs CloudNativePG and builds its fixture once, and returns
// its namespace.
func setupCNPG(t *testing.T) string {
	t.Helper()
	skip(t)
	c.operator(t, "CloudNativePG", "clusters.postgresql.cnpg.io", installCNPG)
	ns := c.namespace(t, "cnpg")
	c.once(t, "cnpg fixture", func(t *testing.T) error {
		t.Helper()
		buildCNPG(t, ns)
		return nil
	})
	return ns
}

// buildCNPG applies the fixture and waits until it is ready.
func buildCNPG(t *testing.T, ns string) {
	t.Helper()
	c.apply(t, ns, cnpgFixture)
	c.kubectl(t, "--namespace", ns, "wait", "cluster/pg", "--for=condition=Ready", "--timeout=10m")
	c.eventually(t, 5*time.Minute, func() error {
		_, err := c.run("--namespace", ns, "rollout", "status", "deployment/pg-pooler-rw", "--timeout=10s")
		return err
	})
	c.eventually(t, 5*time.Minute, func() error {
		out, err := c.run("--namespace", ns, "get", "databases.postgresql.cnpg.io/billing", "--output", "jsonpath={.status.applied}")
		if err == nil && out != "true" {
			err = errNotReady
		}
		return err
	})
}

func TestCNPG(t *testing.T) {
	ns := setupCNPG(t)
	primary := strings.TrimSpace(c.kubectl(t, "--namespace", ns, "get", "pods", "--selector", "cnpg.io/cluster=pg,cnpg.io/instanceRole=primary", "--output", "jsonpath={.items[0].metadata.name}"))
	pooler := strings.TrimSpace(c.kubectl(t, "--namespace", ns, "get", "pods", "--selector", "cnpg.io/poolerName=pg-pooler-rw", "--output", "jsonpath={.items[0].metadata.name}"))

	for _, ch := range []check{
		{url: "k+cnpg://pg." + ns + "/", user: "app", database: "app", kind: "Cluster"},
		{url: "k://pg." + ns + "/", user: "app", database: "app", kind: "Cluster"},
		{url: "k://pg-rw." + ns + "/", user: "app", database: "app", kind: "Service"},
		{url: "k://pg-ro." + ns + "/", user: "app", database: "app", kind: "Service"},
		{url: "k://pg-r." + ns + "/", user: "app", database: "app", kind: "Service"},
		{url: "k://" + primary + "." + ns + "/", user: "app", database: "app", kind: "Pod", name: "primary-pod"},
		{url: "k://reporting@pg." + ns + "/", user: "reporting", database: "app", kind: "Cluster"},
		{url: "k://postgres@pg." + ns + "/", user: "postgres", database: "app", kind: "Cluster"},
		{url: "k://pg-app:@pg." + ns + "/", user: "app", database: "app", kind: "Cluster"},
		{url: "k://pg-reporting:password@pg-rw." + ns + "/", user: "reporting", database: "app", kind: "Service"},
		{url: "k://pg-pooler-rw." + ns + "/", user: "app", database: "app", kind: "Pooler"},
		{url: "k://" + pooler + "." + ns + "/", user: "app", database: "app", kind: "Pod", name: "pooler-pod"},
		{url: "k://billing." + ns + "/", user: "reporting", database: "billing", kind: "Database"},
		{url: "k://pg-rw." + ns + "/billing", user: "app", database: "billing", kind: "Service"},
	} {
		t.Run(ch.folder(), func(t *testing.T) {
			t.Helper()
			c.connect(t, "cnpg", ch)
		})
	}
	t.Run("role conflict", func(t *testing.T) {
		t.Helper()
		c.expectErr(t, "k://pg-ro."+ns+"/?role=rw", dbfwd.ErrRoleConflict)
	})
	t.Run("unknown user", func(t *testing.T) {
		t.Helper()
		c.expectErr(t, "k://nobody@pg."+ns+"/", dbfwd.ErrNotFound)
	})
	t.Run("not found", func(t *testing.T) {
		t.Helper()
		c.expectErr(t, "k://nothing."+ns+"/", dbfwd.ErrNotFound)
	})
	t.Run("failover", func(t *testing.T) {
		// the forward follows the primary when the cluster fails over (D16)
		fwd := c.connect(t, "", check{url: "k://pg-rw." + ns + "/", user: "app", database: "app"})
		old := strings.TrimSpace(c.kubectl(t, "--namespace", ns, "get", "pods", "--selector", "cnpg.io/cluster=pg,cnpg.io/instanceRole=primary", "--output", "jsonpath={.items[0].metadata.name}"))
		// A forced delete is a crash. A graceful one waits for PostgreSQL to
		// shut down, which in CloudNativePG can take longer than the forward
		// waits to reconnect.
		c.kubectl(t, "--namespace", ns, "delete", "pod", old, "--grace-period=0", "--force")
		c.eventually(t, defaultWait, func() error {
			out, err := c.run("--namespace", ns, "get", "pods", "--selector", "cnpg.io/cluster=pg,cnpg.io/instanceRole=primary", "--output", "jsonpath={.items[0].metadata.name}")
			if err == nil && (strings.TrimSpace(out) == "" || strings.TrimSpace(out) == old) {
				err = errNotReady
			}
			return err
		})
		var recovery bool
		retry(t, func() error {
			local, err := fwd.URL(t.Context())
			if err != nil {
				return err
			}
			db, err := sql.Open(local.Driver, local.DSN)
			if err != nil {
				return err
			}
			defer db.Close()
			return db.QueryRowContext(t.Context(), "SELECT pg_is_in_recovery()").Scan(&recovery)
		})
		if recovery {
			t.Errorf("after the failover the forward reached a replica, expected the new primary")
		}
		c.kubectl(t, "--namespace", ns, "wait", "clusters.postgresql.cnpg.io/pg", "--for=condition=Ready", "--timeout=10m")
	})
	t.Run("close", func(t *testing.T) {
		t.Helper()
		checkClose(t, c.connect(t, "", check{url: "k://pg-rw." + ns + "/", user: "app", database: "app"}))
	})
}
