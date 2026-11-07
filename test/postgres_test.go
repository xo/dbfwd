package test

import (
	"testing"
)

// The releases of the PostgreSQL operators of tier 1.
const (
	zalandoRelease   = "v2.0.3"
	pgoRelease       = "v6.0.2"
	stackgresRelease = "1.19.4"
)

func TestZalando(t *testing.T) {
	suite{
		name: "zalando", operator: "Zalando postgres-operator", crd: "postgresqls.acid.zalan.do",
		install: func(t *testing.T) error {
			t.Helper()
			applyKustomize(t, "github.com/zalando/postgres-operator/manifests?ref="+zalandoRelease)
			rollout(t, "default", "deployment/postgres-operator")
			c.eventually(t, defaultWait, func() error {
				_, err := c.run("get", "crd", "postgresqls.acid.zalan.do")
				return err
			})
			return nil
		},
		fixture: `
apiVersion: acid.zalan.do/v1
kind: postgresql
metadata: {name: acid-test}
spec:
  teamId: acid
  numberOfInstances: 1
  volume: {size: 1Gi}
  postgresql: {version: "17"}
  users: {app: []}
  databases: {app: app}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "postgresqls.acid.zalan.do/acid-test", "--for=jsonpath={.status.PostgresClusterStatus}=Running")
			waitPod(t, ns, "cluster-name=acid-test,spilo-role=master")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+zalando://acid-test." + ns + "/", user: "app", database: "app", kind: "postgresql"},
				{url: "k://acid-test." + ns + "/", user: "app", database: "app", kind: "postgresql"},
				{url: "k://acid-test-0." + ns + "/", user: "app", database: "app", kind: "Pod"},
				{url: "k://postgres@acid-test." + ns + "/", user: "postgres", database: "app", kind: "postgresql"},
			}
		},
	}.run(t)
}

func TestCrunchyPGO(t *testing.T) {
	suite{
		name: "pgo", operator: "Crunchy PGO", crd: "postgresclusters.postgres-operator.crunchydata.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyKustomize(t, "github.com/CrunchyData/postgres-operator/config/namespace?ref="+pgoRelease)
			applyKustomize(t, "github.com/CrunchyData/postgres-operator/config/default?ref="+pgoRelease)
			rollout(t, "postgres-operator", "deployment/pgo")
			return nil
		},
		fixture: `
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata: {name: hippo}
spec:
  postgresVersion: 17
  instances:
  - name: instance1
    replicas: 1
    dataVolumeClaimSpec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
  users:
  - {name: app, databases: [app]}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitPod(t, ns, "postgres-operator.crunchydata.com/cluster=hippo,postgres-operator.crunchydata.com/role in (master,primary)")
			c.eventually(t, defaultWait, func() error {
				_, err := c.run("--namespace", ns, "get", "secret", "hippo-pguser-app")
				return err
			})
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			primary := waitPod(t, ns, "postgres-operator.crunchydata.com/cluster=hippo,postgres-operator.crunchydata.com/role in (master,primary)")
			return []check{
				{url: "k+pgo://hippo." + ns + "/", user: "app", database: "app", kind: "PostgresCluster"},
				{url: "k://hippo-primary." + ns + "/", user: "app", database: "app", kind: "Service"},
				{url: "k://" + primary + "." + ns + "/", user: "app", database: "app", kind: "Pod", name: "primary-pod"},
			}
		},
	}.run(t)
}

func TestStackGres(t *testing.T) {
	suite{
		name: "stackgres", operator: "StackGres", crd: "sgclusters.stackgres.io",
		install: func(t *testing.T) error {
			t.Helper()
			// The operator rewrites its own webhooks, so a second helm upgrade
			// conflicts with it. An operator that runs already stays.
			if _, err := c.run("--namespace", "stackgres", "rollout", "status", "deployment/stackgres-operator", "--timeout=5s"); err == nil {
				return nil
			}
			c.helm(t, "repo", "add", "stackgres-charts", "https://stackgres.io/downloads/stackgres-k8s/stackgres/helm/", "--force-update")
			c.helm(t, "upgrade", "--install", "stackgres-operator", "stackgres-charts/stackgres-operator", "--version", stackgresRelease,
				"--namespace", "stackgres", "--create-namespace", "--wait", "--timeout", "15m")
			return nil
		},
		fixture: `
apiVersion: stackgres.io/v1
kind: SGCluster
metadata: {name: simple}
spec:
  profile: development
  instances: 1
  postgres: {version: latest}
  pods: {persistentVolume: {size: 1Gi}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitPod(t, ns, "stackgres.io/cluster-name=simple,role in (primary,master)")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+stackgres://simple." + ns + "/", user: "postgres", database: "postgres", kind: "SGCluster"},
				{url: "k://simple." + ns + "/", user: "postgres", database: "postgres", kind: "SGCluster"},
				{url: "k://simple-0." + ns + "/", user: "postgres", database: "postgres", kind: "Pod"},
			}
		},
	}.run(t)
}
