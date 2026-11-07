package test

import (
	"testing"
)

// mariadbOperatorRelease is the release of mariadb-operator that
// docs/OPERATORS.md was read from.
const mariadbOperatorRelease = "26.10.2"

// installMariaDBOperator installs mariadb-operator from its Helm charts.
func installMariaDBOperator(t *testing.T) error {
	t.Helper()
	c.helm(t, "repo", "add", "mariadb-operator", "https://helm.mariadb.com/mariadb-operator", "--force-update")
	c.helm(t, "upgrade", "--install", "mariadb-operator-crds", "mariadb-operator/mariadb-operator-crds", "--version", mariadbOperatorRelease, "--wait")
	c.helm(t, "upgrade", "--install", "mariadb-operator", "mariadb-operator/mariadb-operator", "--version", mariadbOperatorRelease,
		"--namespace", "mariadb-operator", "--create-namespace", "--wait", "--timeout", "10m")
	return nil
}

const mariadbFixture = `
apiVersion: k8s.mariadb.com/v1alpha1
kind: MariaDB
metadata: {name: maria}
spec:
  username: shop
  database: shop
  storage: {size: 1Gi}
---
apiVersion: k8s.mariadb.com/v1alpha1
kind: MariaDB
metadata: {name: ha}
spec:
  username: shop
  database: shop
  replicas: 2
  replication: {enabled: true}
  storage: {size: 1Gi}
`

func TestMariaDBOperator(t *testing.T) {
	skip(t)
	c.operator(t, "mariadb-operator", "mariadbs.k8s.mariadb.com", installMariaDBOperator)
	ns := c.namespace(t, "mariadb")
	c.apply(t, ns, mariadbFixture)
	c.kubectl(t, "--namespace", ns, "wait", "mariadb/maria", "mariadb/ha", "--for=condition=Ready", "--timeout=15m")
	for _, ch := range []check{
		{url: "k+mariadb-operator://maria." + ns + "/", user: "shop", database: "shop", kind: "MariaDB"},
		{url: "k://maria." + ns + "/", user: "shop", database: "shop", kind: "MariaDB"},
		{url: "k://maria-0." + ns + "/", user: "shop", database: "shop", kind: "Pod"},
		{url: "k://root@maria." + ns + "/", user: "root", database: "shop", kind: "MariaDB"},
		{url: "k://ha." + ns + "/", user: "shop", database: "shop", kind: "MariaDB"},
		{url: "k://ha-primary." + ns + "/", user: "shop", database: "shop", kind: "Service"},
		{url: "k://ha-secondary." + ns + "/", user: "shop", database: "shop", kind: "Service"},
		{url: "k://ha." + ns + "/?role=ro", user: "shop", database: "shop", kind: "MariaDB"},
	} {
		t.Run(ch.folder(), func(t *testing.T) {
			t.Helper()
			c.connect(t, "mariadb-operator", ch)
		})
	}
}
