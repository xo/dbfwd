package test

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// suite is one operator or chart: how to install it, its fixture, how to know
// that the fixture is ready, and the URLs that must reach it.
type suite struct {
	// name names the namespace, dbfwd-<name>, and the folder of the replay
	// files.
	name string

	// operator and crd name what to install. crd is how an existing cluster
	// says that the operator is there. An empty crd needs no operator.
	operator string
	crd      string
	install  func(t *testing.T) error

	// fixture is applied in the namespace.
	fixture string

	// ready waits until the fixture is ready.
	ready func(t *testing.T, ns string)

	// checks returns the URLs that must work, and failures the URLs that
	// must fail, for the namespace.
	checks   func(t *testing.T, ns string) []check
	failures func(ns string) map[string]error

	// cleanup runs before the namespace is deleted, such as to uninstall a
	// Helm release whose cluster-wide objects outlive the namespace.
	cleanup func(ns string)
}

// run runs a suite. On a kind cluster, it deletes its namespace at the end,
// unless DBFWD_TEST_KEEP is set, so that the suites fit on one machine.
func (s suite) run(t *testing.T) {
	t.Helper()
	skip(t)
	if s.crd != "" {
		c.operator(t, s.operator, s.crd, s.install)
	}
	ns := c.namespace(t, s.name)
	if c.existing == "" && os.Getenv("DBFWD_TEST_KEEP") == "" {
		t.Cleanup(func() {
			// the custom resources go first, while their operator still runs
			// to clear their finalizers, which would otherwise hold the
			// namespace in Terminating
			if s.crd != "" {
				_, _ = c.run("--namespace", ns, "delete", s.crd, "--all", "--wait", "--timeout=3m")
			}
			if s.cleanup != nil {
				s.cleanup(ns)
			}
			_, _ = c.run("delete", "namespace", ns, "--wait=false")
		})
	}
	if strings.TrimSpace(s.fixture) != "" {
		c.apply(t, ns, s.fixture)
	}
	s.ready(t, ns)
	for _, ch := range s.checks(t, ns) {
		t.Run(ch.folder(), func(t *testing.T) {
			c.connect(t, s.name, ch)
		})
	}
	if s.failures != nil {
		for raw, want := range s.failures(ns) {
			t.Run(caseName(raw), func(t *testing.T) {
				c.expectErr(t, raw, want)
			})
		}
	}
}

// applyURLs applies published manifests in order.
func applyURLs(t *testing.T, urls ...string) {
	t.Helper()
	for _, u := range urls {
		var err error
		c.eventually(t, defaultWait, func() error {
			_, err = c.run("apply", "--server-side", "--force-conflicts", "--filename", u)
			return err
		})
	}
}

// applyURLsIn applies published manifests in a namespace, which it creates.
func applyURLsIn(t *testing.T, ns string, urls ...string) {
	t.Helper()
	if _, err := c.run("get", "namespace", ns); err != nil {
		c.kubectl(t, "create", "namespace", ns)
	}
	for _, u := range urls {
		c.eventually(t, defaultWait, func() error {
			_, err := c.run("--namespace", ns, "apply", "--server-side", "--force-conflicts", "--filename", u)
			return err
		})
	}
}

// applyKustomize applies a kustomization from a URL.
func applyKustomize(t *testing.T, url string) {
	t.Helper()
	c.eventually(t, defaultWait, func() error {
		_, err := c.run("apply", "--server-side", "--force-conflicts", "--kustomize", url)
		return err
	})
}

// rollout waits until a deployment or a statefulset is ready.
func rollout(t *testing.T, ns, what string) {
	t.Helper()
	c.eventually(t, defaultWait, func() error {
		_, err := c.run("--namespace", ns, "rollout", "status", what, "--timeout=30s")
		return err
	})
}

// waitFor waits until kubectl wait passes for a condition or a JSON path.
func waitFor(t *testing.T, ns string, args ...string) {
	t.Helper()
	c.eventually(t, defaultWait, func() error {
		_, err := c.run(append([]string{"--namespace", ns, "wait", "--timeout=30s"}, args...)...)
		return err
	})
}

// waitPod waits until a pod with the labels is ready, and returns its name.
func waitPod(t *testing.T, ns, selector string) string {
	t.Helper()
	var name string
	c.eventually(t, defaultWait, func() error {
		out, err := c.run("--namespace", ns, "get", "pods", "--selector", selector,
			"--output", `jsonpath={range .items[?(@.status.containerStatuses[0].ready==true)]}{.metadata.name}{"\n"}{end}`)
		if err != nil {
			return err
		}
		name = strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
		if name == "" {
			return errNotReady
		}
		return nil
	})
	return name
}

// fetch downloads a manifest.
func fetch(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetching %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("fetching %s: %s", url, res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// uninstall removes a Helm release, if there is one.
func uninstall(t *testing.T, release, ns string) {
	t.Helper()
	run := func(extra ...string) error {
		args := append([]string{"--kube-context", c.context, "uninstall", release, "--namespace", ns, "--wait"}, extra...)
		cmd := exec.CommandContext(t.Context(), "helm", args...)
		cmd.Env = c.env()
		return cmd.Run()
	}
	// a hook of a release can need a namespace that is already gone
	if err := run(); err != nil {
		_ = run("--no-hooks")
	}
}
