package k8s

import (
	"fmt"

	"github.com/xo/dbfwd"
)

// eck is ECK, Elastic Cloud on Kubernetes, read at v3.5.0. The Secret key of
// the elastic user is the name of the user. HTTPS is on by default, with a
// certificate that the operator signs.
var eck = &operator{
	name:     "ECK",
	key:      "eck",
	kind:     "Elasticsearch",
	groups:   []string{"elasticsearch.k8s.elastic.co"},
	resource: "elasticsearches.elasticsearch.k8s.elastic.co",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		elastic := account{user: literal("elastic"), password: fromSecret(name+"-es-elastic-user", "elastic")}
		return &found{
			scheme:  "elasticsearch",
			forward: "service/" + name + "-es-http",
			port:    9200,
			role:    pick(role),
			tls:     !cr.boolean("spec", "http", "tls", "selfSignedCertificate", "disabled"),
			account: elastic,
			users:   map[string]account{"elastic": elastic},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// openSearch is the OpenSearch Kubernetes Operator, read at v3.0.0. The
// Service has the name that the CR gives it.
var openSearch = &operator{
	name:     "OpenSearch Operator",
	key:      "opensearch-operator",
	kind:     "OpenSearchCluster",
	groups:   []string{"opensearch.org", "opensearch.opster.io"},
	resource: "opensearchclusters.opensearch.org",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := orDefault(cr.str("spec", "security", "config", "adminCredentialsSecret", "name"), name+"-admin-password")
		admin := account{user: fromSecret(secret, "username"), password: fromSecret(secret, "password")}
		admin.user.optional, admin.user.fallback = true, &credential{value: "admin", set: true}
		port := cr.num("spec", "general", "httpPort")
		if port == 0 {
			port = 9200
		}
		tls := cr.get("spec", "security", "tls", "http") != nil && cr.get("spec", "security", "tls", "http", "enabled") != false
		return &found{
			scheme:  "opensearch",
			forward: "service/" + orDefault(cr.str("spec", "general", "serviceName"), name),
			port:    port,
			role:    pick(role),
			tls:     tls,
			account: admin,
			users:   map[string]account{"admin": admin},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// solr is the Apache Solr Operator, read at v0.9.1. Basic auth is off unless
// the CR turns it on, and then the admin password is in the bootstrap Secret.
var solr = &operator{
	name:     "Solr Operator",
	key:      "solr-operator",
	kind:     "SolrCloud",
	groups:   []string{"solr.apache.org"},
	resource: "solrclouds.solr.apache.org",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		f := &found{
			scheme:  "solr",
			forward: "service/" + name + "-solrcloud-common",
			port:    80,
			role:    pick(role),
			users:   map[string]account{},
		}
		if cr.get("spec", "solrTLS") != nil {
			f.port, f.tls = 443, true
		}
		if cr.str("spec", "solrSecurity", "authenticationType") == "Basic" {
			admin := account{user: literal("admin"), password: fromSecret(name+"-solrcloud-security-bootstrap", "admin")}
			f.account, f.users["admin"] = admin, admin
		}
		return f, nil
	},
	roleOf: func(string, string) string { return "" },
}

// altinity is the Altinity clickhouse-operator, read at release-0.27.4. The
// user default has no password and is allowed from loopback, where a
// port-forward arrives.
var altinity = &operator{
	name:     "Altinity ClickHouse Operator",
	key:      "clickhouse-operator",
	kind:     "ClickHouseInstallation",
	groups:   []string{"clickhouse.altinity.com"},
	resource: "clickhouseinstallations.clickhouse.altinity.com",
	build: func(cr object, role string) (*found, error) {
		def := account{user: literal("default")}
		return &found{
			scheme:   "clickhouse",
			forward:  "service/clickhouse-" + cr.name(),
			port:     9000,
			role:     pick(role),
			account:  def,
			users:    map[string]account{"default": def},
			database: literal("default"),
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// cassOperator is cass-operator, read at v1.33.1. The superuser Secret and
// the Service take their names from the cluster name of the datacenter.
var cassOperator = &operator{
	name:     "cass-operator",
	key:      "cass-operator",
	kind:     "CassandraDatacenter",
	groups:   []string{"cassandra.datastax.com"},
	resource: "cassandradatacenters.cassandra.datastax.com",
	build: func(cr object, role string) (*found, error) {
		cluster := cr.str("spec", "clusterName")
		secret := orDefault(cr.str("spec", "superuserSecretName"), cluster+"-superuser")
		su := account{user: fromSecret(secret, "username"), password: fromSecret(secret, "password")}
		return &found{
			scheme:  "cassandra",
			forward: "service/" + cluster + "-" + cr.name() + "-service",
			port:    9042,
			role:    pick(role),
			account: su,
			users:   map[string]account{},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// scylla is Scylla Operator, read at v1.23.0. Auth is off unless scylla.yaml
// turns it on. A Service is owned by a ScyllaDBDatacenter of the same name,
// which the ScyllaCluster owns.
var scylla = &operator{
	name:     "Scylla Operator",
	key:      "scylla-operator",
	kind:     "ScyllaCluster",
	groups:   []string{"scylla.scylladb.com"},
	resource: "scyllaclusters.scylla.scylladb.com",
	build: func(cr object, role string) (*found, error) {
		return &found{
			scheme:  "cassandra",
			forward: "service/" + cr.name() + "-client",
			port:    9042,
			role:    pick(role),
			users:   map[string]account{},
		}, nil
	},
	roleOf:  func(string, string) string { return "" },
	between: map[string]string{"ScyllaDBDatacenter": "scylladbdatacenters.scylla.scylladb.com"},
}

// arango is kube-arangodb, read at 1.4.7. The user root has an empty
// password unless the CR names a Secret, or Auto, which makes
// <name>-root-password. TLS is on by default.
var arango = &operator{
	name:     "kube-arangodb",
	key:      "arangodb-operator",
	kind:     "ArangoDeployment",
	groups:   []string{"database.arangodb.com"},
	resource: "arangodeployments.database.arangodb.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		root := account{user: literal("root")}
		switch s := cr.str("spec", "bootstrap", "passwordSecretNames", "root"); s {
		case "", "None":
		case "Auto":
			root.password = fromSecret(name+"-root-password", "password")
		default:
			root.password = fromSecret(s, "password")
		}
		return &found{
			scheme:  "arangodb",
			forward: "service/" + name,
			port:    8529,
			role:    pick(role),
			tls:     cr.str("spec", "tls", "caSecretName") != "None",
			account: root,
			users:   map[string]account{"root": root},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// crate is crate-operator, read at 2.66.0. The system user has a password in
// a Secret, and the Service serves the PostgreSQL protocol on 5432.
var crate = &operator{
	name:     "CrateDB Operator",
	key:      "crate-operator",
	kind:     "CrateDB",
	groups:   []string{"cloud.crate.io"},
	resource: "cratedbs.cloud.crate.io",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		system := account{user: literal("system"), password: fromSecret("user-system-"+name, "password")}
		return &found{
			scheme:  "cratedb",
			forward: "service/crate-" + name,
			port:    5432,
			role:    pick(role),
			account: system,
			users:   map[string]account{"system": system},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}

// couchbase is the Couchbase Autonomous Operator, read at 2.9.4. The Secret
// that the CR names holds the admin user. The client Service carries only
// the admin port, so a forward goes to a pod that runs the query service, on
// port 8093.
var couchbase = &operator{
	name:     "Couchbase Operator",
	key:      "couchbase-operator",
	kind:     "CouchbaseCluster",
	groups:   []string{"couchbase.com"},
	resource: "couchbaseclusters.couchbase.com",
	build: func(cr object, role string) (*found, error) {
		name := cr.name()
		secret := cr.str("spec", "security", "adminSecret")
		if secret == "" {
			return nil, fmt.Errorf("reading the admin secret of the CouchbaseCluster %s, which names none: %w", name, dbfwd.ErrNotFound)
		}
		admin := account{user: fromSecret(secret, "username"), password: fromSecret(secret, "password")}
		return &found{
			scheme:  "couchbase",
			port:    8093,
			role:    pick(role),
			pods:    "couchbase_cluster=" + name + ",couchbase_service_query=enabled",
			account: admin,
			users:   map[string]account{},
		}, nil
	},
	roleOf: func(string, string) string { return "" },
}
