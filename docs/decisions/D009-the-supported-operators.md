# D9. The supported operators

Status: Decided.

`dbfwd` supports these operators. `docs/OPERATORS.md` holds what each one
creates.

1. PostgreSQL: CloudNativePG, Zalando postgres-operator, Crunchy PGO and
   StackGres.
2. MySQL and MariaDB: mariadb-operator, MOCO, the Oracle MySQL Operator,
   Percona XtraDB Cluster and Percona Server.
3. OpenEverest, which was Percona Everest, with its `pxc` and `postgresql`
   engines. Its `psmdb` engine is MongoDB, which `usql` cannot reach.
4. Elasticsearch: ECK.
5. ClickHouse: the Altinity operator.
6. TiDB: TiDB Operator.
7. OpenSearch: the OpenSearch operator.
8. ScyllaDB: Scylla Operator.
9. Vitess: vitess-operator.
10. Solr: the Solr Operator.
11. Cassandra: cass-operator and K8ssandra.
12. ArangoDB: kube-arangodb.
13. CrateDB: crate-operator.
14. Couchbase: the Couchbase Autonomous Operator.

Each one is a real deployment. It is maintained, it is used, `dbfwd` can find
its credentials, and `usql` can reach its endpoint through a port-forward.

The rules of each operator are read from the upstream source at a named
release, and `docs/OPERATORS.md` names that release. A Secret name changes
with the release and with the values of a chart, so a rule with no release is
not written. Before `dbfwd` supports an operator in code, the operator has a
full section there.

When an operator has no application user, `dbfwd` uses its built-in user:
`root` for OpenEverest `pxc`, and `moco-writable` for MOCO, which has no
default database. A user part in the URL still wins (D6).

Rejected: a table of Secret names with no release.
