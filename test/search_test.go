package test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The releases of the search and analytics operators of tier 1.
const (
	eckRelease        = "3.5.0"
	openSearchChart   = "3.0.14"
	solrRelease       = "0.9.1"
	clickhouseRelease = "release-0.27.4"
)

func TestECK(t *testing.T) {
	suite{
		name: "eck", operator: "ECK", crd: "elasticsearches.elasticsearch.k8s.elastic.co",
		install: func(t *testing.T) error {
			t.Helper()
			base := "https://download.elastic.co/downloads/eck/" + eckRelease + "/"
			applyURLs(t, base+"crds.yaml", base+"operator.yaml")
			rollout(t, "elastic-system", "statefulset/elastic-operator")
			return nil
		},
		fixture: `
apiVersion: elasticsearch.k8s.elastic.co/v1
kind: Elasticsearch
metadata: {name: es}
spec:
  version: 9.2.0
  http: {tls: {selfSignedCertificate: {disabled: true}}}
  nodeSets:
  - name: default
    count: 1
    config: {node.store.allow_mmap: false}
    podTemplate:
      spec:
        containers:
        - name: elasticsearch
          resources: {requests: {memory: 2Gi}, limits: {memory: 2Gi}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "elasticsearches.elasticsearch.k8s.elastic.co/es", "--for=jsonpath={.status.health}=green")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+eck://es." + ns + "/", user: "elastic", database: "", kind: "Elasticsearch"},
				{url: "k://es-es-http." + ns + "/", user: "elastic", database: "", kind: "Service"},
				{url: "k://es-es-default-0." + ns + "/", user: "elastic", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestOpenSearch(t *testing.T) {
	suite{
		name: "opensearch", operator: "OpenSearch Operator", crd: "opensearchclusters.opensearch.org",
		install: func(t *testing.T) error {
			t.Helper()
			c.helm(t, "repo", "add", "opensearch-operator", "https://opensearch-project.github.io/opensearch-k8s-operator/", "--force-update")
			c.helm(t, "upgrade", "--install", "opensearch-operator", "opensearch-operator/opensearch-operator", "--version", openSearchChart,
				"--namespace", "opensearch-operator", "--create-namespace", "--set", "webhook.enabled=false", "--wait", "--timeout", "10m")
			return nil
		},
		fixture: `
apiVersion: opensearch.org/v1
kind: OpenSearchCluster
metadata: {name: os}
spec:
  # the kind node is rootless and cannot set vm.max_map_count, and the host
  # value is high enough
  general: {serviceName: os, version: "3.3.0", setVMMaxMapCount: false}
  security:
    tls:
      transport: {generate: true, perNode: true}
      http: {enabled: false}
  nodePools:
  - component: nodes
    replicas: 1
    diskSize: 2Gi
    roles: [cluster_manager, data]
    resources: {requests: {memory: 2Gi, cpu: 500m}, limits: {memory: 2Gi}}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "opensearchclusters.opensearch.org/os", "--for=jsonpath={.status.phase}=RUNNING")
			// the StatefulSet uses OnDelete, which rollout status does not read
			c.eventually(t, defaultWait, func() error {
				_, err := c.run("--namespace", ns, "wait", "--timeout=30s", "pod/os-nodes-0", "--for=condition=Ready")
				return err
			})
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+opensearch-operator://os." + ns + "/", user: "admin", database: "", kind: "OpenSearchCluster"},
				{url: "k://os-nodes-0." + ns + "/", user: "admin", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestSolr(t *testing.T) {
	suite{
		name: "solr", operator: "Solr Operator", crd: "solrclouds.solr.apache.org",
		install: func(t *testing.T) error {
			t.Helper()
			// The chart comes from the source at the tag, because the Helm
			// repository on solr.apache.org does not always answer. Its
			// ZooKeeper operator comes from charts.pravega.io.
			dir := t.TempDir()
			clone := exec.CommandContext(t.Context(), "git", "clone", "--quiet", "--depth", "1", "--branch", "v"+solrRelease,
				"https://github.com/apache/solr-operator", dir)
			if out, err := clone.CombinedOutput(); err != nil {
				t.Fatalf("cloning the Solr Operator: %v: %s", err, out)
			}
			chart := filepath.Join(dir, "helm", "solr-operator")
			// the ZooKeeper operator needs its CRD, which the usual install
			// takes from solr.apache.org too
			applyURLs(t, "https://raw.githubusercontent.com/pravega/zookeeper-operator/v0.2.15/config/crd/bases/zookeeper.pravega.io_zookeeperclusters.yaml")
			_, _ = c.run("--namespace", "solr-operator", "delete", "deployment", "--all")
			uninstall(t, "solr-operator", "solr-operator")
			c.helm(t, "repo", "add", "pravega", "https://charts.pravega.io", "--force-update")
			c.helm(t, "dependency", "build", chart)
			c.helm(t, "upgrade", "--install", "solr-operator", chart,
				"--namespace", "solr-operator", "--create-namespace", "--wait", "--timeout", "10m")
			return nil
		},
		fixture: `
apiVersion: solr.apache.org/v1beta1
kind: SolrCloud
metadata: {name: solr}
spec:
  replicas: 1
  solrJavaMem: "-Xms512m -Xmx512m"
  solrSecurity: {authenticationType: Basic}
  zookeeperRef:
    provided:
      replicas: 1
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "solrclouds.solr.apache.org/solr", "--for=jsonpath={.status.readyReplicas}=1")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+solr-operator://solr." + ns + "/", user: "admin", database: "", kind: "SolrCloud"},
				{url: "k://solr-solrcloud-common." + ns + "/", user: "admin", database: "", kind: "Service"},
				{url: "k://solr-solrcloud-0." + ns + "/", user: "admin", database: "", kind: "Pod"},
			}
		},
	}.run(t)
}

func TestAltinityClickHouse(t *testing.T) {
	suite{
		name: "clickhouse", operator: "Altinity ClickHouse Operator", crd: "clickhouseinstallations.clickhouse.altinity.com",
		install: func(t *testing.T) error {
			t.Helper()
			applyURLs(t, "https://raw.githubusercontent.com/Altinity/clickhouse-operator/"+clickhouseRelease+"/deploy/operator/clickhouse-operator-install-bundle.yaml")
			rollout(t, "kube-system", "deployment/clickhouse-operator")
			return nil
		},
		fixture: `
apiVersion: clickhouse.altinity.com/v1
kind: ClickHouseInstallation
metadata: {name: ch}
spec:
  defaults:
    templates: {podTemplate: pod}
  templates:
    podTemplates:
    - name: pod
      spec:
        containers:
        - name: clickhouse
          image: clickhouse/clickhouse-server:25.8
  configuration:
    # the kind node has a limit on processes, which the 512 threads of the
    # default schedule pool of ClickHouse exceed
    settings:
      background_schedule_pool_size: 32
    clusters:
    - name: c
      layout: {shardsCount: 1, replicasCount: 1}
`,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			waitFor(t, ns, "clickhouseinstallations.clickhouse.altinity.com/ch", "--for=jsonpath={.status.status}=Completed")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+clickhouse-operator://ch." + ns + "/", user: "default", database: "default", kind: "ClickHouseInstallation"},
				{url: "k://clickhouse-ch." + ns + "/", user: "default", database: "default", kind: "Service"},
				{url: "k://chi-ch-c-0-0-0." + ns + "/", user: "default", database: "default", kind: "Pod"},
			}
		},
	}.run(t)
}
