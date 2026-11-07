# The URL

This document holds the URL that the `k8s` backend reads. D5 holds the
scheme, and D6 holds the rest. A later backend has a URL of its own, and the
rule that a URL holds no password holds for it too (D6).

A person types no user and no password. `dbfwd` reads the cluster resource of
the operator and its Secret, or the container environment when there is no
operator, and finds the user, the password and the database itself. A
password never appears in the URL.

A custom resource, or CR, is an object that an operator adds to Kubernetes. A
cluster CR is the CR that describes one database cluster, such as a
CloudNativePG `Cluster`.

## The form

```text
k[+<operator or type>]://[<user part>@]<name>[.<namespace>][:<port>]/[<dbname>][?<query>]
```

`k` is the short form of the scheme `k8s`, and the examples use it.

```text
k+cnpg://pg.prod/
k+cnpg://pg.prod/billing
k+cnpg://reporting@pg.prod/billing?role=ro
k+cnpg://pg-app:password@pg.prod/app?context=gke_acme_us-east1_main
k+moco://orders.shop/orders
k://pg-rw.prod/
k://my-pg-postgresql-0.dev/
```

## The parts

1. `<operator or type>` is optional. An operator, such as `cnpg`, maps to a
   database scheme, such as `postgres` (D8). A type, such as `postgres`, names
   the database. With neither, `dbfwd` finds it (D7).
2. `<name>` is the name of a cluster CR, a Service, a Helm release, a
   StatefulSet, a Deployment or a Pod. `docs/LOOKUP.md` holds the order and
   how `dbfwd` gets from each one to a database. For a CloudNativePG cluster
   `pg`, `dbfwd` uses the Service `pg-rw`.
3. `<namespace>` is the namespace. If it is not given, `dbfwd` uses the
   namespace of the current context. The host splits at its last dot, because
   a namespace is a DNS label and holds no dot. A name can hold a dot, and
   then the namespace must be given too.
4. `<port>` is the remote port. If it is not given, `dbfwd` reads it from the
   CR, or uses the default of the product.
5. `<dbname>` is the database. If it is not given, `dbfwd` uses the default
   that it finds. For CloudNativePG that is `bootstrap.initdb.database`, which
   is `app` by default.

## The user part

The form of the user part alone decides what it means. `dbfwd` never checks
which of a Secret or a user exists, so a new Secret cannot change what a URL
means.

| Form | Meaning |
| --- | --- |
| nothing | Use the default user that `dbfwd` finds, and its Secret |
| `<user>@` | A database user. `dbfwd` finds the user in the cluster CR or the container environment, and finds the Secret of that user |
| `<secret>:<field>@` | Read the Secret `<secret>` in the namespace. The key `<field>` holds the password |
| `<secret>:@` | The same, with the key `password` |

With the `<secret>:<field>` form, the user comes from the key `username` of
the same Secret. If the Secret has no such key, the user comes from the CR or
the container environment.

A Secret name cannot hold a colon, so the colon is a clear mark. `dburl` keeps
the difference between `secret@` and `secret:@`, because Go's `url.Userinfo`
records that a password was set even when it is empty.

## The query

| Key | Meaning |
| --- | --- |
| `context` | The kube context. If it is not given, `dbfwd` uses the current context of the kubeconfig |
| `role` | `rw` for the primary, which is the default, or `ro` for a replica |

The keys take no prefix. The `k8s` backend takes them out of the query and
passes the rest to the database driver, such as `sslmode` for PostgreSQL. If a driver
ever has a key with the same name, `dbfwd` stops with an error.

The context is in the query and not in the host, because real context names
hold colons, slashes, `@` and dots. An EKS context is an ARN such as
`arn:aws:eks:us-east-1:123:cluster/main`.

If the URL names a Service that implies a role, such as `pg-rw`, and `role`
disagrees, `Resolve` stops with an error.

## The scheme

The scheme is `k8s`, with the aliases `k`, `kc`, `k8`, `kube` and
`kubernetes`.
`dburl` owns that list, and no code here repeats it. `k+cnpg://pg.prod/app`
and `k8s+cnpg://pg.prod/app` are the same URL.

## What dburl gives

`dburl` parses the URL and gives `dbfwd` these fields:

1. `SchemeName` is `k8s`. `Transport` is the operator or the type, passed
   through with no mapping, or empty when the URL has none.
2. `Host`, `Path` and `User` hold the target as it was written. The query
   stays in `RawQuery`, and `dburl` reads no key.
3. `User` keeps the difference between `secret@` and `secret:@`.
4. A URL with no host gives `ErrMissingHost`. `k8s+tcp`, `k8s+unix` and `k8s+`
   give `ErrInvalidTransportProtocol`. `dburl.Open` returns
   `ErrUnresolvedForward`. The DSN and the dialect are empty.

## What usql shows

`usql` shows the target as written in its prompt, with the resolved user and
database. It never shows a Secret name as the user. `\conninfo` shows the
original URL, the resolved user, the database, the context, the namespace and
the local port, from the `Forward` (D11). A Secret name and a key name are not
secret.
