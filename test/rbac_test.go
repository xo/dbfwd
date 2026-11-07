package test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xo/dburl"

	"github.com/xo/dbfwd"
)

// leastRole is the least RBAC of D10 and D16, in one namespace: get on each
// kind that dbfwd reads, list on Services and Pods, watch on Pods, and create
// on pods/portforward. With
// secrets false, it cannot read a Secret.
func leastRole(name string, secrets bool) string {
	core := "services, pods"
	if secrets {
		core += ", secrets"
	}
	return `
apiVersion: v1
kind: ServiceAccount
metadata: {name: ` + name + `}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: ` + name + `}
rules:
- apiGroups: [""]
  resources: [` + core + `]
  verbs: [get]
- apiGroups: [""]
  resources: [services, pods]
  verbs: [list]
- apiGroups: [""]
  resources: [pods]
  verbs: [watch]
- apiGroups: [""]
  resources: [pods/portforward]
  verbs: [create]
- apiGroups: [apps]
  resources: [statefulsets, deployments, replicasets]
  verbs: [get]
- apiGroups: [postgresql.cnpg.io]
  resources: [clusters, poolers, databases]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ` + name + `}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: ` + name + `}
subjects:
- {kind: ServiceAccount, name: ` + name + `}
`
}

// accountKubeconfig writes a kubeconfig that logs in as a ServiceAccount, and
// returns its path.
func accountKubeconfig(t *testing.T, ns, account string) string {
	t.Helper()
	server := c.kubectl(t, "config", "view", "--raw", "--minify", "--output", "jsonpath={.clusters[0].cluster.server}")
	ca := c.kubectl(t, "config", "view", "--raw", "--minify", "--output", "jsonpath={.clusters[0].cluster.certificate-authority-data}")
	token := strings.TrimSpace(c.kubectl(t, "--namespace", ns, "create", "token", account, "--duration", "1h"))
	kubeContext := "dbfwd-" + account
	config := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster: {server: "` + server + `", certificate-authority-data: "` + ca + `"}
users:
- name: ` + account + `
  user: {token: "` + token + `"}
contexts:
- name: ` + kubeContext + `
  context: {cluster: test, user: ` + account + `, namespace: ` + ns + `}
current-context: ` + kubeContext + `
`
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// resolveAs resolves a URL with a kubeconfig of a ServiceAccount.
func resolveAs(t *testing.T, kubeconfig, raw string) (*dbfwd.Forward, error) {
	t.Helper()
	u, err := dburl.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return dbfwd.Resolve(t.Context(), u, dbfwd.WithEnv("KUBECONFIG", kubeconfig))
}

func TestLeastRBAC(t *testing.T) {
	skip(t)
	if c.existing != "" {
		t.Skip("the RBAC test makes ServiceAccounts, which an existing cluster may not allow")
	}
	ns := setupCNPG(t)
	c.apply(t, ns, leastRole("dbfwd-reader", true)+"---\n"+leastRole("dbfwd-blind", false))

	t.Run("least", func(t *testing.T) {
		kubeconfig := accountKubeconfig(t, ns, "dbfwd-reader")
		for _, raw := range []string{"k://pg." + ns + "/", "k://pg-rw." + ns + "/", "k://pg-pooler-rw." + ns + "/"} {
			fwd, err := resolveAs(t, kubeconfig, raw)
			if err != nil {
				t.Fatalf("resolving %s with the least RBAC: %v", raw, err)
			}
			user, database := query(t.Context(), t, fwd)
			if user != "app" || database != "app" {
				t.Errorf("%s connected as %s to %s, expected app to app", raw, user, database)
			}
			_ = fwd.Close()
		}
	})
	t.Run("no secrets", func(t *testing.T) {
		kubeconfig := accountKubeconfig(t, ns, "dbfwd-blind")
		_, err := resolveAs(t, kubeconfig, "k://pg."+ns+"/")
		if err == nil {
			t.Fatal("resolving with no access to secrets worked, expected an error")
		}
		if !errors.Is(err, dbfwd.ErrForbidden) {
			t.Fatalf("resolving with no access to secrets: %v, expected %v", err, dbfwd.ErrForbidden)
		}
	})
}
