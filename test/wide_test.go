package test

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// The releases of the other operators of tier 1.
const (
	cassOperatorRelease = "v1.33.1"
	scyllaRelease       = "v1.23.0"
	arangoRelease       = "1.4.7"
	crateRelease        = "2.66.0"
	couchbaseChart      = "2.94.0"
)

func TestCassOperator(t *testing.T) {
	suite{
		name: "cassandra", operator: "cass-operator", crd: "cassandradatacenters.cassandra.datastax.com",
		install: func(t *testing.T) error {
			t.Helper()
			c.once(t, "cert-manager", installCertManager)
			applyKustomize(t, "github.com/k8ssandra/cass-operator/config/deployments/cluster?ref="+cassOperatorRelease)
			rollout(t, "cass-operator", "deployment/cass-operator-controller-manager")
			return nil
		},
		fixture: `
apiVersion: cassandra.datastax.com/v1beta1
kind: CassandraDatacenter
metadata: {name: dc1}
spec:
  clusterName: test
  serverType: cassandra
  serverVersion: "4.1.8"
  managementApiAuth: {insecure: {}}
  size: 1
  racks: [{name: r1}]
  storageConfig:
    cassandraDataVolumeClaimSpec:
      storageClassName: standard
      accessModes: [ReadWriteOnce]
      resources: {requests: {storage: 1Gi}}
  config:
    jvm-server-options: {initial_heap_size: "512m", max_heap_size: "512m"}
    cassandra-yaml: {authenticator: PasswordAuthenticator, authorizer: CassandraAuthorizer, role_manager: CassandraRoleManager}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "cassandradatacenters.cassandra.datastax.com/dc1", "--for=condition=Ready")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+cass-operator://dc1." + ns + "/", user: "test-superuser", database: "", kind: "CassandraDatacenter"},
				{url: "k://test-dc1-service." + ns + "/", user: "test-superuser", database: "", kind: "Service"},
				{url: "k://test-dc1-r1-sts-0." + ns + "/", user: "test-superuser", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestScylla(t *testing.T) {
	suite{
		name: "scylla", operator: "Scylla Operator", crd: "scyllaclusters.scylla.scylladb.com",
		install: func(t *testing.T) error {
			t.Helper()
			c.once(t, "cert-manager", installCertManager)
			applyURLs(t, "https://raw.githubusercontent.com/scylladb/scylla-operator/"+scyllaRelease+"/deploy/operator.yaml")
			rollout(t, "scylla-operator", "deployment/scylla-operator")
			rollout(t, "scylla-operator", "deployment/webhook-server")
			return nil
		},
		fixture: `
apiVersion: scylla.scylladb.com/v1
kind: ScyllaCluster
metadata: {name: scylla}
spec:
  version: 2025.1.5
  agentVersion: 3.5.1
  developerMode: true
  datacenter:
    name: dc1
    racks:
    - name: r1
      members: 1
      storage: {capacity: 1Gi, storageClassName: standard}
      resources: {requests: {cpu: 10m, memory: 100Mi}, limits: {cpu: 1, memory: 1Gi}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "scyllaclusters.scylla.scylladb.com/scylla", "--for=condition=Available=True")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+scylla-operator://scylla." + ns + "/", user: "", database: "", kind: "ScyllaCluster"},
				{url: "k://scylla-client." + ns + "/", user: "", database: "", kind: "Service"},
				{url: "k://scylla-dc1-r1-0." + ns + "/", user: "", database: "", kind: "Service"},
			}
		},
	}.run(t)
}

func TestArangoDB(t *testing.T) {
	suite{
		name: "arangodb", operator: "kube-arangodb", crd: "arangodeployments.database.arangodb.com",
		install: func(t *testing.T) error {
			t.Helper()
			// the operator watches only its own namespace, so it runs in the
			// namespace of the suite
			c.waitGone(t, "dbfwd-arangodb")
			args := []string{"upgrade", "--install", "kube-arangodb",
				"https://github.com/arangodb/kube-arangodb/releases/download/" + arangoRelease + "/kube-arangodb-" + arangoRelease + ".tgz",
				"--namespace", "dbfwd-arangodb", "--create-namespace", "--wait", "--timeout", "10m"}
			// a running operator takes over its CRDs, so a later install
			// leaves them alone
			if _, err := c.run("get", "crd", "arangodeployments.database.arangodb.com"); err == nil {
				args = append(args, "--skip-crds")
			}
			c.helm(t, args...)
			return nil
		},
		fixture: `
apiVersion: database.arangodb.com/v1
kind: ArangoDeployment
metadata: {name: adb}
spec:
  mode: Single
  image: arangodb/arangodb:3.12.2
  externalAccess: {type: None}
  tls: {caSecretName: None}
  bootstrap: {passwordSecretNames: {root: Auto}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "arangodeployments.database.arangodb.com/adb", "--for=condition=Ready")
			// the operator writes the root password a moment after Ready
			c.eventually(t, defaultWait, func() error {
				_, err := c.run("--namespace", ns, "get", "secret", "adb-root-password")
				return err
			})
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			pod := waitPod(t, ns, "arango_deployment=adb")
			return []check{
				{url: "k+arangodb-operator://adb." + ns + "/", user: "root", database: "", kind: "ArangoDeployment"},
				{url: "k://adb." + ns + "/", user: "root", database: "", kind: "ArangoDeployment"},
				{url: "k://" + pod + "." + ns + "/", user: "root", database: "", kind: "Pod", name: "pod"},
			}
		},
	}.run(t)
}

func TestCrateDB(t *testing.T) {
	suite{
		name: "cratedb", operator: "CrateDB Operator", crd: "cratedbs.cloud.crate.io",
		install: func(t *testing.T) error {
			t.Helper()
			c.helm(t, "repo", "add", "crate-operator", "https://crate.github.io/crate-operator", "--force-update")
			c.helm(t, "upgrade", "--install", "crate-operator", "crate-operator/crate-operator", "--version", crateRelease,
				"--namespace", "crate-operator", "--create-namespace",
				"--set", "env.CRATEDB_OPERATOR_DEBUG_VOLUME_STORAGE_CLASS=standard",
				"--set", "env.CRATEDB_OPERATOR_DEBUG_VOLUME_SIZE=1GiB",
				"--set", "env.CRATEDB_OPERATOR_CLOUD_PROVIDER=", "--wait", "--timeout", "10m")
			return nil
		},
		fixture: `
apiVersion: cloud.crate.io/v1
kind: CrateDB
metadata: {name: crate1}
spec:
  cluster: {imageRegistry: crate, name: crate1, version: 6.0.3, exposure: clusterip}
  nodes:
    data:
    - name: hot
      replicas: 1
      resources:
        heapRatio: 0.25
        disk: {count: 1, size: 1GiB, storageClass: standard}
        limits: {cpu: 1, memory: 1Gi}
        requests: {cpu: 0.5, memory: 1Gi}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			// The kind node is rootless, so the privileged init container that
			// sets vm.max_map_count fails, and the host value is high enough.
			// The operator has no switch for it, so the test takes it out of
			// the StatefulSet.
			const sts = "statefulset/crate-data-hot-crate1"
			c.eventually(t, defaultWait, func() error {
				_, err := c.run("--namespace", ns, "get", sts)
				return err
			})
			c.kubectl(t, "--namespace", ns, "patch", sts, "--type=json",
				`--patch=[{"op":"remove","path":"/spec/template/spec/initContainers/0"}]`)
			_, _ = c.run("--namespace", ns, "delete", "pod", "crate-data-hot-crate1-0", "--wait=false")
			waitFor(t, ns, "cratedbs.cloud.crate.io/crate1", "--for=jsonpath={.status.crateDBStatus.health}=GREEN")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+crate-operator://crate1." + ns + "/", user: "system", database: "", kind: "CrateDB"},
				{url: "k://crate-crate1." + ns + "/", user: "system", database: "", kind: "Service"},
				{url: "k://crate-data-hot-crate1-0." + ns + "/", user: "system", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestCouchbase(t *testing.T) {
	suite{
		name: "couchbase",
		ready: func(t *testing.T, ns string) {
			t.Helper()
			// the chart installs the operator and the cluster together
			c.helm(t, "repo", "add", "couchbase", "https://couchbase-partners.github.io/helm-charts/", "--force-update")
			// The chart applies the cluster before its admission webhook
			// answers, so the operator and the webhook go first.
			args := []string{"upgrade", "--install", "cb", "couchbase/couchbase-operator", "--version", couchbaseChart, "--namespace", ns,
				"--set", "cluster.name=cb", "--set", "cluster.servers.default.size=1",
				"--set", "cluster.servers.default.services={data,index,query}", "--wait", "--timeout", "15m"}
			c.helm(t, append(args, "--set", "install.couchbaseCluster=false")...)
			c.helm(t, args...)
			// a new cluster takes longer than the default wait
			c.eventually(t, 25*time.Minute, func() error {
				_, err := c.run("--namespace", ns, "wait", "--timeout=30s", "couchbaseclusters.couchbase.com/cb", "--for=condition=Available")
				return err
			})
			// the cluster is Available before its pod is ready, and dbfwd
			// forwards only to a ready pod (D16)
			waitFor(t, ns, "pod", "--selector", "couchbase_cluster=cb", "--for=condition=Ready")
		},
		cleanup: func(ns string) {
			cmd := exec.CommandContext(context.Background(), "helm", "--kube-context", c.context, "uninstall", "cb", "--namespace", ns, "--wait")
			cmd.Env = c.env()
			_ = cmd.Run()
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+couchbase-operator://cb." + ns + "/", user: "Administrator", database: "", kind: "CouchbaseCluster"},
				{url: "k://cb-0000." + ns + "/", user: "Administrator", database: "", kind: "Service"},
			}
		},
	}.run(t)
}
