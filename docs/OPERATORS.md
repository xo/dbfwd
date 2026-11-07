# Operators

This document holds what each supported operator creates, and where `dbfwd`
reads the user, the password, the database and the Service. Every fact was
read from the upstream source at the release named. A model answer is not a
source.

D9 lists the supported operators. Four have a full section here. The rest are
in the tier 1 table, and each gets a full section before `dbfwd` supports it
in code. A Secret name changes with the release and with the values of a
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

This table is a summary. Each operator gets a full section like the first
four before `dbfwd` supports it in code.

| Database | Operator | Stars | Cluster CR | Credentials | Service and port |
| --- | --- | --- | --- | --- | --- |
| PostgreSQL | Zalando postgres-operator | 5272 | `postgresql`, `acid.zalan.do/v1` | `{user}.{cluster}.credentials.postgresql.acid.zalan.do` | `<name>`, 5432 |
| PostgreSQL | Crunchy PGO | 4458 | `PostgresCluster`, `postgres-operator.crunchydata.com/v1` | `<name>-pguser-<user>`, keys `user`, `password`, `dbname` | `<name>-primary`, 5432 |
| PostgreSQL | StackGres | 1444 | `SGCluster`, `stackgres.io/v1` | `<name>`, keys `superuser-username`, `superuser-password` | `<name>`, 5432 |
| Elasticsearch | ECK, from Elastic | 2855 | `Elasticsearch`, `elasticsearch.k8s.elastic.co/v1` | `<name>-es-elastic-user`, key `elastic`, user `elastic` | `<name>-es-http`, 9200 |
| ClickHouse | Altinity clickhouse-operator | 2581 | `ClickHouseInstallation`, `clickhouse.altinity.com/v1` | User `default`, empty password, allowed from loopback | `clickhouse-<name>`, 9000 and 8123 |
| TiDB | TiDB Operator, from PingCAP | 1342 | `TidbCluster`, `pingcap.com/v1alpha1` | User `root`, empty password unless a `TidbInitializer` sets one | `<name>-tidb`, 4000 |
| MySQL | Oracle MySQL Operator | 947 | `InnoDBCluster`, `mysql.oracle.com/v2` | The Secret named in `spec.secretName` | `<name>`, 3306 |
| OpenSearch | OpenSearch operator, from the project | 589 | `OpenSearchCluster`, `opensearch.org/v1` | `<name>-admin-password`, keys `username`, `password` | `spec.general.serviceName`, 9200 |
| MySQL | Percona PXC and Percona Server | 625 and 234 | `PerconaXtraDBCluster`, `PerconaServerMySQL` | `<name>-secrets` | `<name>-haproxy`, 3306 |
| ScyllaDB | Scylla Operator, from ScyllaDB | 401 | `ScyllaCluster`, `scylla.scylladb.com/v1` | Auth is off by default | `<name>-client`, 9042 |
| Vitess | vitess-operator, from PlanetScale | 374 | `VitessCluster`, `planetscale.com/v2` | None unless static auth is set | `status.gatewayServiceName`, 3306 |
| Solr | Solr Operator, from Apache | 285 | `SolrCloud`, `solr.apache.org/v1beta1` | Off by default. With Basic auth, `<name>-solrcloud-basic-auth` | `<name>-solrcloud-common`, 80 |
| Cassandra | cass-operator and K8ssandra | 228 and 243 | `CassandraDatacenter`, `cassandra.datastax.com/v1beta1` | `<cluster>-superuser`, keys `username`, `password` | `<cluster>-<dc>-service`, 9042 |
| ArangoDB | kube-arangodb, from ArangoDB | 235 | `ArangoDeployment`, `database.arangodb.com/v1` | User `root`, empty password by default. TLS on | `<name>`, 8529 |
| CrateDB | crate-operator, from Crate | 27 | `CrateDB`, `cloud.crate.io/v1` | `user-system-<name>`, key `password`, user `system` | `crate-<name>`, 5432 |
| Couchbase | Couchbase Autonomous Operator | mirror | `CouchbaseCluster`, `couchbase.com/v2` | The Secret named in `spec.security.adminSecret` | `<name>-srv`, query 8093 |

The star counts of CrateDB and Couchbase are low because the vendor builds
them, and the public repository is small or a mirror. Both vendors publish
them as the supported way to run on Kubernetes.

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
