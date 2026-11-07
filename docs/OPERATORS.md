# Operators

This document holds what each supported operator creates, and where `dbfwd`
reads the user, the password, the database and the Service. Every fact was
read from the upstream source at the release named. A model answer is not a
source.

D9 lists the supported operators. Four have a full section here, and the
rest have a full row in the tier 1 table. A Secret name changes with the release and with the values of a
chart, so each section names the release it was read from. Read the source
again when a new release arrives.

## CloudNativePG

Release v1.30.1, from `cloudnative-pg/cloudnative-pg`. The scheme maps to
`postgres`.

| Item | Value |
| --- | --- |
| Cluster CR | `Cluster`, `postgresql.cnpg.io/v1` |
| Default user | `bootstrap.initdb.owner`. It defaults to the database name, so `app` |
| Default database | `bootstrap.initdb.database`, default `app` |
| Secret of the default user | `<cluster>-app`, unless `bootstrap.initdb.secret.name` names another |
| Secret keys | `username`, `password`, `dbname`, `host`, `port`, `uri` and others. The type is `kubernetes.io/basic-auth` |
| Other users | `spec.managed.roles`, and the `DatabaseRole` CR from v1.30. Each has `passwordSecret` |
| Superuser | `<cluster>-superuser`, user `postgres`. It exists only when `enableSuperuserAccess` is true, and the default is false |
| Service | `<cluster>-rw` for the primary, `<cluster>-ro` for replicas, `<cluster>-r` for any instance, port 5432 |
| Pooler | `Pooler`, `postgresql.cnpg.io/v1`. `spec.cluster.name` names the cluster, and `spec.type` is `rw`, `ro` or `r`. Its Service has its name and the port 5432, and the Service and the Deployment have an owner reference to it. PgBouncer checks a password against the cluster, so the accounts of the cluster work through it |
| Database | `Database`, `postgresql.cnpg.io/v1`. `spec.cluster.name`, `spec.name` is the database, and `spec.owner` is its owner. It has no Secret |

## mariadb-operator

Release 26.10.2, from `mariadb-operator/mariadb-operator`. The scheme maps to
`mariadb`.

| Item | Value |
| --- | --- |
| Cluster CR | `MariaDB`, `k8s.mariadb.com/v1alpha1`. Release v0.0.25 and earlier used `mariadb.mmontes.io/v1alpha1` |
| Default user | `spec.username`. It has no default. If it is not set, the only user is `root` |
| Default database | `spec.database`. It has no default |
| Secret of the user | `spec.passwordSecretKeyRef`. It defaults to `<name>-password`, key `password` |
| Secret of root | `spec.rootPasswordSecretKeyRef`. It defaults to `<name>-root`, key `password` |
| Other users | The `User` CR, with `passwordSecretKeyRef` |
| Service | `<name>-primary` when replication or Galera is on, else `<name>`. Port `spec.port`, default 3306 |

## MOCO

Release v0.38.0, from `cybozu-go/moco`. The scheme maps to `mysql`.

| Item | Value |
| --- | --- |
| Cluster CR | `MySQLCluster`, `moco.cybozu.com/v1beta2` |
| Default user | `moco-writable` (D9). MOCO has no application user. It creates `moco-writable` and `moco-readonly`, and its docs use `moco-writable` to create a database |
| Default database | None. A URL with no database connects with none |
| Secret | `moco-<name>` in the namespace of the cluster. Keys `WRITABLE_PASSWORD`, `READONLY_PASSWORD`, `ADMIN_PASSWORD` and others |
| Service | `moco-<name>-primary` and `moco-<name>-replica`, port 3306 |

## OpenEverest

Percona Everest is now OpenEverest. The repositories moved to
`openeverest/openeverest` and `openeverest/openeverest-operator`. Release v1.17.0
of the operator. It manages other Percona operators, so the scheme maps to the
engine of each cluster.

| Item | Value |
| --- | --- |
| Cluster CR | `DatabaseCluster`, `everest.percona.com/v1alpha1` |
| Engine | `spec.engine.type`: `pxc`, `postgresql` or `psmdb` |
| Secret | `spec.engine.userSecretsName`, default `everest-secrets-<name>` |
| User for `pxc` | `root`, from the key `root` |
| User for `postgresql` | `postgres`. The keys follow the Percona PostgreSQL format. Not read yet |
| User for `psmdb` | Not supported. It is MongoDB, which `usql` cannot reach (D9) |
| Service | `status.hostname` and `status.port`. For `pxc` that is `<name>-haproxy` or `<name>-proxysql`. For `postgresql` it is `<name>-pgbouncer` when that is on |

OpenEverest v2 is a developer preview. v1 goes on in the `v1.x` branch.

Notes for OpenEverest:

1. The `pxc` default user is `root` (D9). It is the strongest user, and the
   only one Everest names.
2. The keys of the PostgreSQL Secret are not read yet.

## From a Service or a Pod to the cluster

`dbfwd` follows owner references from a Service or a Pod up to the cluster CR
(D7). An owner reference is the field `metadata.ownerReferences`, and the one
with `controller: true` names the object that manages it. These were read from
the upstream source at the releases above, and at PXC v1.20.0, Percona
PostgreSQL v3.1.0 and Percona MongoDB v1.23.1.

| Operator | Service to cluster | Pod to cluster | Primary pod |
| --- | --- | --- | --- |
| CloudNativePG | Owner is the `Cluster`. Label `cnpg.io/cluster` | Owner is the `Cluster`. There is no StatefulSet | Label `cnpg.io/instanceRole=primary` |
| mariadb-operator | Owner is the `MariaDB`. The Service has no label of the operator, so the owner is the only link | StatefulSet `<name>`, whose owner is the `MariaDB` | Label `k8s.mariadb.com/role=primary` |
| MOCO | Owner is the `MySQLCluster`. Label `app.kubernetes.io/created-by=moco` | StatefulSet `moco-<name>`, whose owner is the `MySQLCluster` | Label `moco.cybozu.com/role=primary` |
| OpenEverest | No owner reference to the `DatabaseCluster` | No owner reference to the `DatabaseCluster` | Depends on the engine |

The database container is `postgres` for CloudNativePG, `mariadb` for
mariadb-operator, and `mysqld` for MOCO. A MOCO pod also holds
`mysqld-exporter`, which is not the database.

OpenEverest needs two steps. `dbfwd` follows the owner to the Percona CR,
then reads the `DatabaseCluster` with the same name and namespace. Everest
gives the Percona CR the name of the `DatabaseCluster`.

| Engine under OpenEverest | Service to Percona CR | Primary |
| --- | --- | --- |
| `pxc` | Owner is the `PerconaXtraDBCluster`. `<name>-haproxy` is the write endpoint | No label marks the primary pod. Use the HAProxy Service |
| `postgresql` | Owner is the `PostgresCluster`, whose owner is the `PerconaPGCluster`, so there is one more step | Label `postgres-operator.crunchydata.com/role=primary`, or `master` on older releases. Service `<name>-primary` |

## Every database that usql reaches

These were read from the upstream source on 2026-11-07, for every database
that `usql` reaches and that runs on Kubernetes. Tier 1 is supported (D9).
Tier 2 is not. Tier 3 needs no operator code, and each database in it is
reached once `docs/LOOKUP.md` holds its pod spec rules.

An operator or a chart counts only if it is a real deployment. It meets four
tests:

1. It is maintained: a release or commits in the last six months.
2. It is used: the database vendor or a CNCF or Apache project publishes it,
   or it has adoption well above a hobby project.
3. `dbfwd` can find the credentials from the CR, from a Secret with a fixed
   name, or from the container environment.
4. It has an endpoint that `usql` can reach through a port-forward.

### Tier 1: supported operators

Each row was read from the upstream source at the release named, and has
rules in the `k8s` package and a suite in the `test` module. "Pods" is the
label selector of the pods of a role, which `dbfwd` uses when a Service has
no selector because the operator writes its endpoints.

| Operator, release | Cluster CR | Default user and database | Secret | Service, port | Pods, and notes |
| --- | --- | --- | --- | --- | --- |
| Zalando postgres-operator, v2.0.3 | `postgresql`, `acid.zalan.do/v1` | The owner of the first database in `spec.databases`, else `postgres`. Database: that one, else `postgres` | `{user}.{cluster}.credentials.postgresql.acid.zalan.do`, keys `username`, `password`. `_` in a user becomes `-` | `<name>` read-write, with no selector. `<name>-repl` read-only. 5432 | `cluster-name=<name>,spilo-role=master` or `replica`. No owner reference by default, so the labels `application=spilo` and `cluster-name` name the cluster |
| Crunchy PGO, v6.0.2 | `PostgresCluster`, `postgres-operator.crunchydata.com/v1beta1` | The first of `spec.users`, and its first database. With none, a user and a database named after the cluster | `<name>-pguser-<user>`, keys `user`, `password`, `dbname` | `<name>-primary`, with no selector. `<name>-replicas`. `spec.port`, default 5432 | `postgres-operator.crunchydata.com/role` is `master` or `primary`, or `replica` |
| StackGres, 1.19.4 | `SGCluster`, `stackgres.io/v1` | `postgres` from the Secret, database `postgres` | `<name>`, keys `superuser-username`, `superuser-password` | `<name>`, with no selector. `<name>-replicas`. 5432 on the pod | `stackgres.io/cluster-name=<name>,role` is `primary` or `master`, or `replica` |
| MySQL Operator, 26.10.0-2.3.1 | `InnoDBCluster`, `mysql.oracle.com/v2` | The `rootUser` key of the Secret, else `root`. No database | `spec.secretName`, which the person makes, keys `rootUser`, `rootPassword` | `<name>`, the Router. 3306 read-write, 6447 read-only | The images come from `container-registry.oracle.com` |
| Percona XtraDB Cluster, v1.20.0 | `PerconaXtraDBCluster`, `pxc.percona.com/v1` | `root`. No database | `spec.secretsName`, default `<name>-secrets`, key `root` | `<name>-haproxy`, or `<name>-proxysql`. `<name>-haproxy-replicas`. 3306 | OpenEverest `pxc` makes one of these, so the rule serves both |
| Percona Server for MySQL, v1.3.0 | `PerconaServerMySQL`, `ps.percona.com/v1` | `root`. No database | `spec.secretsName`, default `<name>-secrets`, key `root` | `<name>-haproxy`, 3306 read-write and 3307 read-only. `<name>-router` when only the Router is on | `mysql.percona.com/primary=true` |
| TiDB Operator, v1.6.6 | `TidbCluster`, `pingcap.com/v1alpha1` | `root`, with no password unless `spec.tidb.initializer.createPassword`. No database | `<name>-init`, key `root`, only with the initializer | `<name>-tidb`, 4000, only when `spec.tidb.service` is set | `app.kubernetes.io/component=tidb,app.kubernetes.io/instance=<name>`. Every TiDB pod is a SQL node |
| Vitess Operator, v2.17.1 | `VitessCluster`, `planetscale.com/v2` | The first user of the `users.json` file of vtgate. Database: the first keyspace | The Secret in `cells[].gateway.authentication.static.secret`, key `users.json` by default | `status.gatewayServiceName`, 3306 | vtgate needs static auth, so the fixture sets it |
| ECK, 3.5.0 | `Elasticsearch`, `elasticsearch.k8s.elastic.co/v1` | `elastic` | `<name>-es-elastic-user`, key `elastic` | `<name>-es-http`, 9200 | HTTPS is on by default. dbfwd adds `tls=true` |
| OpenSearch Operator, v3.0.0 | `OpenSearchCluster`, `opensearch.org/v1` | `admin` | `spec.security.config.adminCredentialsSecret`, default `<name>-admin-password`, keys `username`, `password` | `spec.general.serviceName`, `spec.general.httpPort` default 9200 | HTTPS when `spec.security.tls.http` is on |
| Solr Operator, v0.9.1 | `SolrCloud`, `solr.apache.org/v1beta1` | `admin`, when `spec.solrSecurity.authenticationType` is `Basic`. Else no auth | `<name>-solrcloud-security-bootstrap`, key `admin` | `<name>-solrcloud-common`, 80, or 443 with TLS | |
| Altinity ClickHouse Operator, release-0.27.4 | `ClickHouseInstallation`, `clickhouse.altinity.com/v1` | `default`, with no password, allowed from loopback, where a port-forward arrives. Database `default` | None | `clickhouse-<name>`, 9000 native | |
| cass-operator, v1.33.1 | `CassandraDatacenter`, `cassandra.datastax.com/v1beta1` | `<clusterName>-superuser`, from the Secret | `spec.superuserSecretName`, default `<clusterName>-superuser`, keys `username`, `password` | `<clusterName>-<dc>-service`, 9042 | |
| Scylla Operator, v1.23.0 | `ScyllaCluster`, `scylla.scylladb.com/v1` | None. Auth is off unless scylla.yaml turns it on | None | `<name>-client`, 9042 | A Service is owned by a `ScyllaDBDatacenter` of the same name, which the `ScyllaCluster` owns |
| kube-arangodb, 1.4.7 | `ArangoDeployment`, `database.arangodb.com/v1` | `root`, with no password unless `spec.bootstrap.passwordSecretNames.root` names a Secret, or `Auto` | `<name>-root-password` for `Auto`, key `password` | `<name>`, 8529 | Pods are owned by the CR, with no StatefulSet. TLS is on unless `spec.tls.caSecretName` is `None` |
| CrateDB Operator, 2.66.0 | `CrateDB`, `cloud.crate.io/v1` | `system` | `user-system-<name>`, key `password` | `crate-<name>`, 5432 for the PostgreSQL protocol | |
| Couchbase Operator, 2.9.4 | `CouchbaseCluster`, `couchbase.com/v2` | The user of the admin Secret | `spec.security.adminSecret`, which the person makes, keys `username`, `password` | The client Service carries only the admin port, so dbfwd forwards to a pod, on 8093 for queries | `couchbase_cluster=<name>,couchbase_service_query=enabled`. Pods are owned by the CR. The chart runs the Enterprise image |

A TLS endpoint gets `tls=true` in the local URL, which the drivers of `usql`
read. The certificate names the Services of the cluster and not
`127.0.0.1`, so a driver that checks it refuses the connection. The fixtures
turn TLS off.

### Tier 2: operators with a limit

| Database | Operator | Limit |
| --- | --- | --- |
| Oracle | oracle-database-operator, from Oracle | The only stored password is for `SYS` and `SYSTEM`. The PDB comes from `status.pdbConnectString` |
| Vertica | vertica-kubernetes, from Vertica | The public repository had no commit after 2025-12. Packaged charts still ship |
| Trino, Druid | Stackable operators | Small adoption. Users live in a Secret that the operator never reads |
| ClickHouse | ClickHouse operator, from ClickHouse | Pre-1.0, release v0.0.8. Only a headless Service |
| TiDB | TiDB Operator v2 | Password handling not read yet |
| KubeBlocks | KubeBlocks | One operator for many engines, with a Secret per component |

### Tier 3: no operator, but a common chart

`dbfwd` reaches these through a Service or a Helm release and the container
environment (D7). A chart sets no owner reference. None of them has its pod
spec rules in `docs/LOOKUP.md` yet.

| Database | Chart | Credentials | Service and port |
| --- | --- | --- | --- |
| Trino | `trinodb/charts` | Off by default | `<release>-trino`, 8080 |
| Neo4j | `neo4j/helm-charts`, from Neo4j | `<name>-auth`, key `NEO4J_AUTH` holds `neo4j/<password>` | `<release>`, bolt 7687 |
| Pinot | the chart in `apache/pinot` | Off by default | `<release>-pinot-broker`, 8099 |
| InfluxDB 3 | `influxdata/helm-charts` | A token, if `adminToken.existingSecret` is set | `<release>`, 8181 |
| QuestDB | `questdb/questdb-kubernetes` | Built-in `admin` and `quest` | `<release>`, 8812 for PostgreSQL wire, if exposed |
| Databend | `databendlabs/helm-charts` | User `root`. Password not confirmed | `<release>`, 3307 MySQL wire |
| SurrealDB | `surrealdb/helm-charts` | `SURREAL_USER` and `SURREAL_PASS` in the environment | `<release>`, 8000 |
| rqlite | `rqlite/helm-charts` | Off unless `config.users` is set | `<release>`, 80 |
| Dremio | the Dremio OCI chart | No default user found | `dremio-client`, 31010 |
| SQL Server | none with real adoption | `MSSQL_SA_PASSWORD` or `SA_PASSWORD` in the environment | 1433 |

### Rejected

| Database | Why |
| --- | --- |
| SingleStore | The operator is closed source, and it stores only a hash of the admin password |
| CockroachDB | The old operator is deprecated. The new one defaults to TLS client certificates, and `dbfwd` must then write them to files |
| Presto | The chart had no change in a year |
| VoltDB | The chart is proprietary, and its source cannot be read |
| YDB | The operator had no commit in eight months |
| Hive, Impala, Drill, Avatica, Exasol, SAP HANA, Firebird, libSQL | No maintained operator or chart. A hand-written StatefulSet still works through the container environment |
| MongoDB, Redis | `usql` has no driver for either |
