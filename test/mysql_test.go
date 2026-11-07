package test

import (
	"testing"
)

// The releases of the MySQL operators of tier 1.
const (
	oracleMySQLRelease = "26.10.0-2.3.1"
	pxcRelease         = "v1.20.0"
	psRelease          = "v1.3.0"
	tidbRelease        = "v1.6.6"
	vitessRelease      = "v2.17.1"
)

func TestOracleMySQLOperator(t *testing.T) {
	base := "https://raw.githubusercontent.com/mysql/mysql-operator/" + oracleMySQLRelease + "/deploy/"
	suite{
		name: "mysql-operator", operator: "MySQL Operator", crd: "innodbclusters.mysql.oracle.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyURLs(t, base+"deploy-crds.yaml", base+"deploy-operator.yaml")
			// container-registry.oracle.com is the only source of these images,
			// and it can stall, so a pull that does not finish skips the suite
			if _, err := c.run("--namespace", "mysql-operator", "rollout", "status", "deployment/mysql-operator", "--timeout=15m"); err != nil {
				t.Skipf("the image of the MySQL Operator did not arrive from container-registry.oracle.com in 15 minutes: %v", err)
			}
			return nil
		},
		fixture: `
apiVersion: v1
kind: Secret
metadata: {name: mypwds}
stringData: {rootUser: root, rootHost: "%", rootPassword: root-test-only}
---
apiVersion: mysql.oracle.com/v2
kind: InnoDBCluster
metadata: {name: mycluster}
spec:
  secretName: mypwds
  tlsUseSelfSigned: true
  instances: 1
  router: {instances: 1}
  datadirVolumeClaimTemplate:
    accessModes: [ReadWriteOnce]
    resources: {requests: {storage: 1Gi}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "innodbclusters.mysql.oracle.com/mycluster", "--for=jsonpath={.status.cluster.status}=ONLINE")
			rollout(t, ns, "deployment/mycluster-router")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+mysql-operator://mycluster." + ns + "/", user: "root", database: "", kind: "InnoDBCluster"},
				{url: "k://mycluster." + ns + "/", user: "root", database: "", kind: "InnoDBCluster"},
				{url: "k://mycluster-0." + ns + "/", user: "root", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestPerconaXtraDB(t *testing.T) {
	suite{
		name: "pxc", operator: "Percona XtraDB Cluster", crd: "perconaxtradbclusters.pxc.percona.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyURLsIn(t, "pxc-operator", "https://raw.githubusercontent.com/percona/percona-xtradb-cluster-operator/"+pxcRelease+"/deploy/cw-bundle.yaml")
			rollout(t, "pxc-operator", "deployment/percona-xtradb-cluster-operator")
			return nil
		},
		fixture: `
apiVersion: pxc.percona.com/v1
kind: PerconaXtraDBCluster
metadata: {name: shop}
spec:
  crVersion: 1.20.0
  unsafeFlags: {tls: true, pxcSize: true, proxySize: true}
  tls: {enabled: false}
  pxc:
    size: 1
    image: percona/percona-xtradb-cluster:8.0.43-34.1
    volumeSpec:
      persistentVolumeClaim: {resources: {requests: {storage: 1Gi}}}
  haproxy:
    enabled: true
    size: 1
    image: percona/haproxy:2.8.15
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "perconaxtradbclusters.pxc.percona.com/shop", "--for=jsonpath={.status.state}=ready")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+pxc://shop." + ns + "/", user: "root", database: "", kind: "PerconaXtraDBCluster"},
				{url: "k://shop-haproxy." + ns + "/", user: "root", database: "", kind: "Service"},
				{url: "k://shop-pxc-0." + ns + "/", user: "root", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestPerconaServer(t *testing.T) {
	suite{
		name: "ps", operator: "Percona Server for MySQL", crd: "perconaservermysqls.ps.percona.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyURLsIn(t, "ps-operator", "https://raw.githubusercontent.com/percona/percona-server-mysql-operator/"+psRelease+"/deploy/cw-bundle.yaml")
			rollout(t, "ps-operator", "deployment/percona-server-mysql-operator")
			return nil
		},
		fixture: `
apiVersion: ps.percona.com/v1
kind: PerconaServerMySQL
metadata: {name: shop}
spec:
  crVersion: 1.3.0
  unsafeFlags: {mysqlSize: true, proxySize: true}
  mysql:
    clusterType: group-replication
    size: 1
    image: percona/percona-server:8.4.6-6.1
    volumeSpec:
      persistentVolumeClaim: {resources: {requests: {storage: 1Gi}}}
  proxy:
    haproxy: {enabled: true, size: 1, image: percona/haproxy:2.8.15}
    router: {enabled: false}
  orchestrator: {enabled: false}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "perconaservermysqls.ps.percona.com/shop", "--for=jsonpath={.status.state}=ready")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+ps://shop." + ns + "/", user: "root", database: "", kind: "PerconaServerMySQL"},
				{url: "k://shop-haproxy." + ns + "/", user: "root", database: "", kind: "Service"},
				{url: "k://shop-mysql-0." + ns + "/", user: "root", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestTiDB(t *testing.T) {
	suite{
		name: "tidb", operator: "TiDB Operator", crd: "tidbclusters.pingcap.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyURLs(t, "https://raw.githubusercontent.com/pingcap/tidb-operator/"+tidbRelease+"/manifests/crd.yaml")
			c.helm(t, "repo", "add", "pingcap", "https://charts.pingcap.com", "--force-update")
			c.helm(t, "upgrade", "--install", "tidb-operator", "pingcap/tidb-operator", "--version", tidbRelease,
				"--namespace", "tidb-admin", "--create-namespace", "--wait", "--timeout", "10m")
			return nil
		},
		fixture: `
apiVersion: pingcap.com/v1alpha1
kind: TidbCluster
metadata: {name: basic}
spec:
  version: v8.5.4
  timezone: UTC
  pvReclaimPolicy: Delete
  enableDynamicConfiguration: true
  configUpdateStrategy: RollingUpdate
  discovery: {}
  pd:
    baseImage: pingcap/pd
    replicas: 1
    requests: {storage: 1Gi}
    config: {}
  tikv:
    baseImage: pingcap/tikv
    replicas: 1
    requests: {storage: 1Gi}
    config: {}
  tidb:
    baseImage: pingcap/tidb
    replicas: 1
    service: {type: ClusterIP}
    config: {}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "tidbclusters.pingcap.com/basic", "--for=condition=Ready")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+tidb-operator://basic." + ns + "/", user: "root", database: "", kind: "TidbCluster"},
				{url: "k://basic-tidb." + ns + "/", user: "root", database: "", kind: "Service"},
				{url: "k://basic-tidb-0." + ns + "/", user: "root", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}
