package test

import (
	"testing"
)

// The releases that the MOCO suite installs. MOCO needs cert-manager.
const (
	mocoRelease        = "0.38.0"
	certManagerRelease = "1.21.3"
	mocoMySQL          = "ghcr.io/cybozu-go/moco/mysql:8.4.10"
)

// installCertManager installs cert-manager from its published manifest.
func installCertManager(t *testing.T) error {
	t.Helper()
	url := "https://github.com/cert-manager/cert-manager/releases/download/v" + certManagerRelease + "/cert-manager.yaml"
	if _, err := c.run("apply", "--server-side", "--force-conflicts", "--filename", url); err != nil {
		return err
	}
	for _, d := range []string{"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"} {
		if _, err := c.run("--namespace", "cert-manager", "rollout", "status", "deployment/"+d, "--timeout=5m"); err != nil {
			return err
		}
	}
	return nil
}

// installMOCO installs MOCO from its published manifest.
func installMOCO(t *testing.T) error {
	t.Helper()
	c.once(t, "cert-manager", installCertManager)
	url := "https://github.com/cybozu-go/moco/releases/download/v" + mocoRelease + "/moco.yaml"
	var err error
	c.eventually(t, defaultWait, func() error {
		_, err = c.run("apply", "--server-side", "--force-conflicts", "--filename", url)
		return err
	})
	_, err = c.run("--namespace", "moco-system", "rollout", "status", "deployment/moco-controller", "--timeout=5m")
	return err
}

const mocoFixture = `
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata: {name: orders}
spec:
  replicas: 1
  podTemplate:
    spec:
      securityContext: {fsGroup: 10000, fsGroupChangePolicy: OnRootMismatch}
      containers:
      - name: mysqld
        image: ` + mocoMySQL + `
  volumeClaimTemplates:
  - metadata: {name: mysql-data}
    spec:
      accessModes: [ReadWriteOnce]
      resources: {requests: {storage: 1Gi}}
`

func TestMOCO(t *testing.T) {
	skip(t)
	c.operator(t, "MOCO", "mysqlclusters.moco.cybozu.com", installMOCO)
	ns := c.namespace(t, "moco")
	c.apply(t, ns, mocoFixture)
	c.kubectl(t, "--namespace", ns, "wait", "mysqlcluster/orders", "--for=condition=Available", "--timeout=15m")
	for _, ch := range []check{
		{url: "k+moco://orders." + ns + "/", user: "moco-writable", database: "", kind: "MySQLCluster"},
		{url: "k://orders." + ns + "/", user: "moco-writable", database: "", kind: "MySQLCluster"},
		{url: "k://moco-orders-primary." + ns + "/", user: "moco-writable", database: "", kind: "Service"},
		{url: "k://moco-orders-0." + ns + "/", user: "moco-writable", database: "", kind: "Pod"},
		{url: "k://moco-readonly@orders." + ns + "/", user: "moco-readonly", database: "", kind: "MySQLCluster"},
		{url: "k://moco-admin@orders." + ns + "/", user: "moco-admin", database: "", kind: "MySQLCluster"},
	} {
		t.Run(ch.folder(), func(t *testing.T) {
			t.Helper()
			c.connect(t, "moco", ch)
		})
	}
}
