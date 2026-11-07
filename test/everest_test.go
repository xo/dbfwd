package test

import (
	"slices"
	"strings"
	"testing"
)

// everestChart is the release of the OpenEverest Helm chart, which carries
// operator v1.17.0.
const everestChart = "1.17.0"

// TestOpenEverest installs OpenEverest with only its pxc engine, which makes a
// PerconaXtraDBCluster for each DatabaseCluster. It runs its database
// operators in its own namespace, so it uses no suite namespace of its own.
func TestOpenEverest(t *testing.T) {
	const ns = "dbfwd-everest"
	suite{
		name: "everest", operator: "OpenEverest", crd: "databaseclusters.everest.percona.com",
		install: func(t *testing.T) error {
			t.Helper()
			c.helm(t, "repo", "add", "openeverest", "https://openeverest.github.io/helm-charts/", "--force-update")
			// An install that runs already stays, because uninstalling it
			// strands objects that only its operator can remove.
			if _, err := c.run("--namespace", ns, "rollout", "status", "deployment/percona-xtradb-cluster-operator", "--timeout=5s"); err == nil {
				return nil
			}
			// A failed earlier install leaves a release that cannot be
			// upgraded. With OLM and the operator gone, nothing removes the
			// finalizers of what they made, so the test removes them.
			uninstall(t, "everest-core", "everest-system")
			unfinalize("clusterserviceversions.operators.coreos.com", "--namespace", ns)
			unfinalize("podschedulingpolicies.everest.percona.com")
			for _, n := range []string{ns, "everest-olm", "everest-system"} {
				c.waitGone(t, n)
			}
			c.helm(t, "upgrade", "--install", "everest-core", "openeverest/openeverest", "--version", everestChart,
				"--namespace", "everest-system", "--create-namespace",
				"--set", "dbNamespace.namespaceOverride="+ns,
				"--set", "dbNamespace.postgresql=false", "--set", "dbNamespace.psmdb=false",
				// the operator needs the monitoring CRDs, so monitoring stays on
				"--set", "telemetry=false",
				"--wait", "--timeout", "15m")
			rollout(t, ns, "deployment/percona-xtradb-cluster-operator")
			return nil
		},
		fixture: `
apiVersion: everest.percona.com/v1alpha1
kind: DatabaseCluster
metadata: {name: shop}
spec:
  engine:
    type: pxc
    replicas: 1
    storage: {size: 1Gi}
    resources: {cpu: 600m, memory: 1G}
  proxy:
    type: haproxy
    replicas: 1
    expose: {type: internal}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "databaseclusters.everest.percona.com/shop", "--for=jsonpath={.status.status}=ready")
			// The operator restarts the HAProxy pod after the cluster first
			// reports ready, and dbfwd forwards only to a ready pod (D16).
			// The statefulsets update on delete, so the test waits for the
			// pods and not for a rollout.
			waitPod(t, ns, "app.kubernetes.io/instance=shop,app.kubernetes.io/component=pxc")
			waitPod(t, ns, "app.kubernetes.io/instance=shop,app.kubernetes.io/component=haproxy")
			waitFor(t, ns, "databaseclusters.everest.percona.com/shop", "--for=jsonpath={.status.status}=ready")
			waitFor(t, ns, "pod", "--selector", "app.kubernetes.io/instance=shop,app.kubernetes.io/component=haproxy", "--for=condition=Ready")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+everest://shop." + ns + "/", user: "root", database: "", kind: "DatabaseCluster"},
				{url: "k://shop." + ns + "/", user: "root", database: "", kind: "DatabaseCluster"},
				{url: "k://shop-haproxy." + ns + "/", user: "root", database: "", kind: "Service"},
			}
		},
	}.run(t)
}

// unfinalize removes the finalizers of every object of a kind and deletes
// it. scope holds flags such as --namespace.
func unfinalize(kind string, scope ...string) {
	kubectl := func(args ...string) (string, error) {
		return c.run(append(slices.Clone(scope), args...)...)
	}
	out, _ := kubectl("get", kind, "--output", "name")
	for name := range strings.FieldsSeq(out) {
		_, _ = kubectl("patch", name, "--type=merge", "--patch", `{"metadata":{"finalizers":null}}`)
		_, _ = kubectl("delete", name, "--wait=false")
	}
}
