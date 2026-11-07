# Lookup

This document holds how `dbfwd` finds a database from the name in a URL.
Given `k://<name>.<namespace>`, `dbfwd` makes every attempt it can to connect.
D7 holds the decision.

## The name

The host of the URL is `<name>.<namespace>`, split at the last dot. If the
namespace is not given, `dbfwd` uses the namespace of the current context.
`dbfwd` never searches across namespaces, because that needs the right to list
in every namespace.

The type after the `+` is optional. `k://pg-rw.prod/` has no type, and
`dbfwd` finds it. `k+cnpg://pg.prod/` has one, and `dbfwd` reads only the
kinds of that operator. A type such as `postgres` says what the database is,
and `dbfwd` uses it in place of what it detects.

## The order

`dbfwd` tries each kind in this order, and the first match wins. A later kind
is never tried after a match, so the result does not change when an object of
another kind appears.

1. A cluster CR of a supported operator, such as a CloudNativePG `Cluster`.
2. Another resource of an operator that names a cluster: a CloudNativePG
   `Pooler`, a CloudNativePG `Database`, or a mariadb-operator `MaxScale`.
   Their owner chains are not yet read from the upstream source. That comes
   before this step is written.
3. A Service.
4. A Helm release. `dbfwd` lists the Services in the namespace that carry the
   label `app.kubernetes.io/instance=<name>`. It must find exactly one
   database Service, or it stops and names the candidates.
5. A StatefulSet or a Deployment. For a StatefulSet, `dbfwd` uses the pod
   with ordinal 0, and an operator that owns it picks the primary. For a
   Deployment, `kubectl port-forward` picks the pod.
6. A Pod.

With no type, step 1 and step 2 cover every operator that `dbfwd` supports.
Most clusters have only some of those kinds installed. `dbfwd` asks the
cluster once which kinds exist, and skips a kind that is not installed. A kind
that is not installed is not an error.

The `Forward` says which kind matched and what owns it, such as "Service
pg-rw, owned by CloudNativePG Cluster pg". `usql` shows that in `\conninfo`.

## From a match to a database

A match from step 3, 5 or 6 can belong to an operator. `dbfwd` follows the
owner references of the object up to a cluster CR. If it reaches one, it uses
the rules of that operator in `docs/OPERATORS.md`, and the Service or Pod
picks the role: `pg-rw` is the primary, and `pg-ro` is a replica. If the URL
also gives a `role` that disagrees, `Resolve` stops with an error (D6).

OpenEverest sets no owner reference to its `DatabaseCluster`. So when the walk
ends at a Percona CR, `dbfwd` reads the `DatabaseCluster` with the same name.
`docs/OPERATORS.md` holds the owner chain of each operator.

If no operator owns it, `dbfwd` reads the pod spec. A Service leads to its pods
through its selector. `dbfwd` then finds the database container, the type, the
user, the password and the database.

### The database container

A pod can hold more than one container. An exporter for metrics often carries
the same variables as the database, so a variable alone does not pick the
container. `dbfwd` picks the container whose image or port is a database, in
this order:

1. The image name: `postgres`, `postgresql`, `mysql`, `mariadb`, `mongo`,
   `mongodb`, `mssql/server`, `clickhouse`, `redis` or `valkey`. The registry
   and the tag do not count.
2. The port that the Service targets.
3. A port with a known name or number: 5432, 3306, 27017, 1433, 6379, 8123 or
   9000.

An image named `exporter`, `pgbouncer` or `proxy` is never the database
container.

### The type

`dbfwd` finds the type from the database container, in this order:

1. The prefix of an environment variable: `POSTGRES_`, `MYSQL_`, `MARIADB_`,
   `MONGO_INITDB_` or `MONGODB_`, `MSSQL_` or `SA_PASSWORD`, `CLICKHOUSE_`, and
   `REDIS_`. This works on a mirrored image.
2. The image name.
3. The port.
4. The label `app.kubernetes.io/name`, which a chart sets to its own name.

If the signals disagree, `dbfwd` stops and asks for a type, such as
`k+postgres://`. It never guesses from a port alone.

### The credentials

The official images and the common charts pass the user and the password as
environment variables. A variable reaches a Secret in one of three ways, and
`dbfwd` follows all three:

1. `valueFrom.secretKeyRef`: the variable names the Secret and the key.
2. A `_FILE` variable, such as `POSTGRES_PASSWORD_FILE`: the path leads to a
   volume mount, the mount leads to a Secret volume, and the base name of the
   file is the key. Bitnami charts do this by default.
3. `envFrom.secretRef`: the whole Secret becomes variables, so `dbfwd` matches
   the keys of the Secret against the known names.

A plain `value` on the variable is used as it is.

| Type | User | Password | Database |
| --- | --- | --- | --- |
| postgres | `POSTGRES_USER`, else `postgres` | `POSTGRES_PASSWORD`, or `POSTGRES_POSTGRES_PASSWORD` for `postgres` on Bitnami | `POSTGRES_DB` or `POSTGRES_DATABASE`, else the user |
| mysql | `MYSQL_USER`, else `root` | `MYSQL_PASSWORD`, or `MYSQL_ROOT_PASSWORD` for `root` | `MYSQL_DATABASE` |
| mariadb | `MARIADB_USER`, else `root` | `MARIADB_PASSWORD`, or `MARIADB_ROOT_PASSWORD` for `root` | `MARIADB_DATABASE` |
| mongodb | `MONGO_INITDB_ROOT_USERNAME` or `MONGODB_ROOT_USER` | `MONGO_INITDB_ROOT_PASSWORD` or `MONGODB_ROOT_PASSWORD` | `admin` for the login |
| sqlserver | `sa` | `MSSQL_SA_PASSWORD` or `SA_PASSWORD` | `master` |
| clickhouse | `CLICKHOUSE_ADMIN_USER` or `CLICKHOUSE_USER`, else `default` | `CLICKHOUSE_ADMIN_PASSWORD` or `CLICKHOUSE_PASSWORD` | `default` |

These six types are the ones that the pod spec rules cover. A database of
another type with no operator, such as Neo4j or Trino from a chart, needs its
row here before `dbfwd` can reach it. `docs/OPERATORS.md` lists the candidates
in tier 3.

`usql` has no MongoDB driver, so the `mongodb` row and the MongoDB charts
below are for detection only. `dbfwd` reports the type and stops.

A user part in the URL wins over what `dbfwd` finds. With `<user>@`, `dbfwd`
reads the password variable of that user. With `<secret>:<field>@`, it reads
that Secret.

## Helm charts

These were read from the chart source on 2026-11-07. `F` is the full name of
the release: `<release>-<chart>`, or `<release>` when the release name holds
the chart name.

| Chart | Service | Workload | Secret and keys | How the password is passed |
| --- | --- | --- | --- | --- |
| bitnami/postgresql | `F`, `F-hl` | StatefulSet `F` | `F`: `postgres-password`, `password` | `_FILE` by default |
| bitnami/mysql | `F`, `F-headless` | StatefulSet `F` | `F`: `mysql-root-password`, `mysql-password` | `_FILE` by default |
| bitnami/mariadb | `F`, `F-headless` | StatefulSet `F` | `F`: `mariadb-root-password`, `mariadb-password` | `_FILE` by default |
| bitnami/mongodb | `F` | Deployment `F` | `F`: `mongodb-root-password` | `_FILE` by default |
| bitnami/clickhouse | `F`, `F-headless` | StatefulSet `F-shard0` | `F`: `admin-password` | `_FILE` or `secretKeyRef` |
| cloudpirates/postgres | `F`, `F-headless` | StatefulSet `F` | `F`: `postgres-password` | `secretKeyRef` |
| cloudpirates/mariadb | `F`, `F-headless` | StatefulSet `F` | `F`: `mariadb-root-password`, `mariadb-password` | `secretKeyRef` |
| cloudpirates/mongodb | `F`, `F-headless` | StatefulSet `F` | `F`: `mongodb-root-password` | `secretKeyRef`, user `admin` as a plain value |
| groundhog2k postgres, mysql, mariadb | `F` | StatefulSet `F` | `F`: keys named after the variables | `secretKeyRef` for the user, the password and the database |
| groundhog2k/mongodb | `F`, `F-headless` | StatefulSet `F` | `F`: keys named after the variables | `envFrom` |

All three families set `app.kubernetes.io/name` to the chart name and
`app.kubernetes.io/instance` to the release.

Bitnami stopped building free versioned images on 2025-08-28. The tags moved
to `bitnamilegacy`, which gets no updates. The chart source is still active,
and many clusters still run it, so `dbfwd` supports it. CloudPirates and
groundhog2k use the official images.

No standalone chart for SQL Server or ClickHouse has real adoption. The
variables of the official images still cover a hand-written StatefulSet.

## Not looked up

1. Ingress, Gateway and EndpointSlice. They route HTTP, or point at pods that
   a Service already reaches.
2. A prefix of a name, such as `pg` for `pg-1`. It hides a mistake. A
   StatefulSet lookup covers the real case.
3. An ExternalName Service. It points outside the cluster, and a port-forward
   cannot reach it. `dbfwd` says so.

## The error

If every step fails, the error names what `dbfwd` tried, such as "no
CloudNativePG Cluster, Service, Helm release, StatefulSet, Deployment or Pod
named pg in namespace prod". If a step was refused by RBAC, the error says
that step was not allowed, apart from not found. It never holds a password or
the content of a Secret.
