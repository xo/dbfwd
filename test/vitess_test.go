package test

import (
	"strings"
	"testing"
)

// vitessFixture is a VitessCluster with one cell, one keyspace and one tablet,
// from the example of the operator at v2.17.1. vtgate reads the user from the
// Secret, so dbfwd reads it too.
const vitessFixture = `
apiVersion: v1
kind: Secret
metadata: {name: vt-config}
stringData:
  users.json: |
    {"user": [{"UserData": "user", "Password": ""}]}
  init_db.sql: |
    # This file is executed immediately after initializing a fresh data directory.

    ###############################################################################
    # Equivalent of mysql_secure_installation
    ###############################################################################

    # We need to ensure that super_read_only is disabled so that we can execute
    # these commands. Note that disabling it does NOT disable read_only.
    # We save the current value so that we only re-enable it at the end if it was
    # enabled before.
    SET @original_super_read_only=IF(@@global.super_read_only=1, 'ON', 'OFF');
    SET GLOBAL super_read_only='OFF';

    # Changes during the init db should not make it to the binlog.
    # They could potentially create errant transactions on replicas.
    SET sql_log_bin = 0;
    # Remove anonymous users & disable remote root access (only allow UNIX socket).
    DROP USER IF EXISTS ''@'%', ''@'localhost', 'root'@'%';

    # Remove test database.
    DROP DATABASE IF EXISTS test;

    ###############################################################################
    # Vitess defaults
    ###############################################################################

    # Vitess-internal database.
    CREATE DATABASE IF NOT EXISTS _vt;

    # Admin user with all privileges.
    CREATE USER 'vt_dba'@'localhost';
    GRANT ALL ON *.* TO 'vt_dba'@'localhost';
    GRANT GRANT OPTION ON *.* TO 'vt_dba'@'localhost';
    GRANT PROXY ON ''@'' TO 'vt_dba'@'localhost' WITH GRANT OPTION;

    # User for app traffic, with global read-write access.
    CREATE USER 'vt_app'@'localhost';
    GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, RELOAD, PROCESS, FILE,
    REFERENCES, INDEX, ALTER, SHOW DATABASES, CREATE TEMPORARY TABLES,
    LOCK TABLES, EXECUTE, REPLICATION CLIENT, CREATE VIEW,
    SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, CREATE USER, EVENT, TRIGGER
    ON *.* TO 'vt_app'@'localhost';

    # User for app debug traffic, with global read access.
    CREATE USER 'vt_appdebug'@'localhost';
    GRANT SELECT, SHOW DATABASES, PROCESS ON *.* TO 'vt_appdebug'@'localhost';

    # User for administrative operations that need to be executed as non-SUPER.
    # Same permissions as vt_app here.
    CREATE USER 'vt_allprivs'@'localhost';
    GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, RELOAD, PROCESS, FILE,
    REFERENCES, INDEX, ALTER, SHOW DATABASES, CREATE TEMPORARY TABLES,
    LOCK TABLES, EXECUTE, REPLICATION SLAVE, REPLICATION CLIENT, CREATE VIEW,
    SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, CREATE USER, EVENT, TRIGGER
    ON *.* TO 'vt_allprivs'@'localhost';

    # User for slave replication connections.
    CREATE USER 'vt_repl'@'%';
    GRANT REPLICATION SLAVE ON *.* TO 'vt_repl'@'%';

    # User for Vitess VReplication (base vstreamers and vplayer).
    CREATE USER 'vt_filtered'@'localhost';
    GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, RELOAD, PROCESS, FILE,
    REFERENCES, INDEX, ALTER, SHOW DATABASES, CREATE TEMPORARY TABLES,
    LOCK TABLES, EXECUTE, REPLICATION SLAVE, REPLICATION CLIENT, CREATE VIEW,
    SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, CREATE USER, EVENT, TRIGGER
    ON *.* TO 'vt_filtered'@'localhost';

    # User for general MySQL monitoring.
    CREATE USER 'vt_monitoring'@'localhost';
    GRANT SELECT, PROCESS, SUPER, REPLICATION CLIENT, RELOAD
    ON *.* TO 'vt_monitoring'@'localhost';
    GRANT SELECT, UPDATE, DELETE, DROP
    ON performance_schema.* TO 'vt_monitoring'@'localhost';

    # custom sql is used to add custom scripts like creating users/passwords. We use it in our tests
    # {{custom_sql}}

    # We need to set super_read_only back to what it was before
    SET GLOBAL super_read_only=IFNULL(@original_super_read_only, 'ON');
---
apiVersion: planetscale.com/v2
kind: VitessCluster
metadata: {name: vt}
spec:
  images:
    vtctld: vitess/lite:v23.0.6-mysql80
    vtgate: vitess/lite:v23.0.6-mysql80
    vttablet: vitess/lite:v23.0.6-mysql80
    vtorc: vitess/lite:v23.0.6-mysql80
    vtbackup: vitess/lite:v23.0.6-mysql80
    mysqld: {mysql80Compatible: vitess/lite:v23.0.6-mysql80}
    mysqldExporter: prom/mysqld-exporter:v0.14.0
  cells:
  - name: zone1
    gateway:
      authentication: {static: {secret: {name: vt-config, key: users.json}}}
      replicas: 1
      resources: {requests: {cpu: 100m, memory: 256Mi}, limits: {memory: 256Mi}}
  vitessDashboard:
    cells: [zone1]
    replicas: 1
    resources: {requests: {cpu: 100m, memory: 128Mi}, limits: {memory: 128Mi}}
  keyspaces:
  - name: commerce
    durabilityPolicy: none
    turndownPolicy: Immediate
    vitessOrchestrator:
      resources: {requests: {cpu: 100m, memory: 128Mi}, limits: {memory: 128Mi}}
    partitionings:
    - equal:
        parts: 1
        shardTemplate:
          databaseInitScriptSecret: {name: vt-config, key: init_db.sql}
          tabletPools:
          - cell: zone1
            type: replica
            replicas: 1
            vttablet:
              resources: {requests: {cpu: 100m, memory: 256Mi}, limits: {memory: 256Mi}}
            mysqld:
              resources: {requests: {cpu: 100m, memory: 512Mi}, limits: {memory: 1024Mi}}
            dataVolumeClaimTemplate:
              accessModes: [ReadWriteOnce]
              resources: {requests: {storage: 1Gi}}
  updateStrategy: {type: Immediate}
`

func TestVitess(t *testing.T) {
	suite{
		name: "vitess", operator: "Vitess Operator", crd: "vitessclusters.planetscale.com",
		install: func(t *testing.T) error {
			t.Helper()
			// The operator watches only its own namespace, and its manifests
			// name the namespaces example and default, so they are applied
			// with the namespace of the suite in their place.
			ns := "dbfwd-vitess"
			c.waitGone(t, ns)
			if _, err := c.run("get", "namespace", ns); err != nil {
				c.kubectl(t, "create", "namespace", ns)
			}
			base := "https://raw.githubusercontent.com/planetscale/vitess-operator/" + vitessRelease + "/deploy/"
			files := []string{"crds/planetscale.com_vitessclusters.yaml", "crds/planetscale.com_vitesscells.yaml",
				"crds/planetscale.com_vitesskeyspaces.yaml", "crds/planetscale.com_vitessshards.yaml",
				"crds/planetscale.com_vitessbackups.yaml", "crds/planetscale.com_vitessbackupstorages.yaml",
				"crds/planetscale.com_etcdlockservers.yaml", "crds/planetscale.com_vitessbackupschedules.yaml",
				"priority.yaml", "service_account.yaml", "role.yaml", "role_binding.yaml", "operator.yaml"}
			for _, f := range files {
				manifest := strings.ReplaceAll(fetch(t, base+f), "namespace: example", "namespace: "+ns)
				// the ClusterRoleBinding names the namespace default
				manifest = strings.ReplaceAll(manifest, "namespace: default", "namespace: "+ns)
				manifest = strings.ReplaceAll(manifest, "planetscale/vitess-operator:latest", "planetscale/vitess-operator:"+vitessRelease)
				c.apply(t, ns, manifest)
			}
			rollout(t, ns, "deployment/vitess-operator")
			return nil
		},
		fixture: vitessFixture,
		ready: func(t *testing.T, ns string) {
			t.Helper()
			c.eventually(t, 2*defaultWait, func() error {
				out, err := c.run("--namespace", ns, "get", "vitessclusters.planetscale.com/vt", "--output", "jsonpath={.status.gatewayServiceName}")
				if err == nil && out == "" {
					err = errNotReady
				}
				return err
			})
			waitPod(t, ns, "planetscale.com/component=vtgate")
			waitPod(t, ns, "planetscale.com/component=vttablet")
		},
		checks: func(t *testing.T, ns string) []check {
			t.Helper()
			return []check{
				{url: "k+vitess-operator://vt." + ns + "/", user: "user", database: "commerce", kind: "VitessCluster"},
				{url: "k://vt." + ns + "/", user: "user", database: "commerce", kind: "VitessCluster"},
			}
		},
	}.run(t)
}
