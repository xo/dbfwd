# Listing the databases in a cluster

Status: built, as D18 describes. `dbfwd.List` and the command `dbfwd list`
do what this document says. The `\k` command of `usql` calls the same
`List`.

`usql` asks for a meta command, such as `\k`, that lists the databases that
`dbfwd` can reach, with one row for each database and the URL to pass to
`\c`. This document is the side of `dbfwd`: the API, what a row is, what it
reads, what it costs, and what RBAC it needs.

## The scope is a URL

A listing takes the same URL as `Resolve`, with `*` in the name. The host
splits at its last dot into a name and a namespace, as it does for `Resolve`
(`docs/URL.md`), so `*` fits in both parts.

| URL | Lists |
| --- | --- |
| `k://*` | every database in the namespace of the current context |
| `k://*.prod` | every database in `prod` |
| `k://*.*` | every database in every namespace |
| `k://pg*.prod` | the databases in `prod` whose name starts with `pg` |
| `k+cnpg://*.*` | every CloudNativePG cluster, in every namespace |
| `k+postgres://*.prod` | every PostgreSQL database in `prod`, of any operator or none |
| `k://*.prod?context=kind-x` | every database in `prod`, in the context `kind-x` |

This was checked with `dburl` v0.57.0. It parses every form above. It
rejects a URL with no host, such as `k://` or `k8s://?context=x`, so the
current namespace needs `k://*` and not an empty host.

`*` is the only wildcard. `?` and `[...]` are not allowed in the host of a
URL, so a caller with a richer pattern, such as one of `psql`, lists with `*`
and filters the rows itself.

A user part, a port, a database in the path and the key `role` mean nothing
to a listing, and it fails with `ErrBadURL` when the URL has one. An empty
path and `/` are both allowed, because each URL that a listing returns ends
in `/`, and a user can paste one back as a scope.

A type after the `+` can be an alias that `dburl` knows, as it can for
`Resolve` (`docs/URL.md`), so `k+pg://*.prod` lists the PostgreSQL databases
in `prod`.

## The API

```go
// List lists the databases that u names. The name and the namespace in u
// can be "*", and the name can hold "*" as a wildcard. List reads no
// credential and starts no forward.
func List(ctx context.Context, u *dburl.URL, opts ...Option) (*Listing, error)

// Listing is what List found.
type Listing struct {
	// Scope is where List looked, as Target.Scope holds it, with the
	// context and the namespace resolved, such as the current context when
	// the URL named none. With "*" as the namespace, the namespace is "*".
	// A caller names it in a title, or when Entries is empty.
	Scope []Field

	// Entries holds one entry for each database, sorted by scope, then by
	// name.
	Entries []Entry

	// Skipped holds each part of the scope that List did not read, such
	// as a namespace or a kind where RBAC does not allow list.
	Skipped []Skip
}

// Entry is one database.
type Entry struct {
	// Target is the object that the row stands for, as Lookup reports
	// it.
	Target Target

	// Operator is the name of the operator that manages the database, such
	// as "CloudNativePG", or empty when none does. Target.Owner names the
	// object at the top of the chain, such as "CloudNativePG Cluster pg",
	// which for a cluster CR is the row itself.
	Operator string

	// URL is the shortest URL that Resolve maps to the same database, such
	// as "k://pg.prod/". It holds no password.
	URL string

	// Roles holds the roles that the database serves, such as "rw" and "ro".
	Roles []string

	// Ready is the number of pods that are ready, and Pods the number of
	// pods.
	Ready, Pods int

	// Databases holds the databases that the operator declares, or none.
	Databases []string

	// Credential says where Resolve reads the password, such as
	// "Secret pg-app". It holds no secret value.
	Credential string
}

// Skip is a part of the scope that List did not read.
type Skip struct {
	// Scope is where, such as the namespace "prod".
	Scope []Field

	// Kind is what List tried to list there, such as "Services", or empty
	// for every kind.
	Kind string

	// Reason says why, such as "not allowed".
	Reason string
}
```

A backend that can list implements one more interface, which the root
package defines where it calls it:

```go
// Lister is a Backend that can list its databases.
type Lister interface {
	List(ctx context.Context, u *dburl.URL, cfg Config) (*Listing, error)
}
```

`List` on a backend that is not a `Lister` returns `ErrUnsupported`. `List`
returns an error only when it read nothing, or when RBAC forbids the scope
that the URL asked for. When it read some of the scope, it returns the
entries and lists the rest in `Skipped`, so that a caller can print the
table and then a note.

`Operator` is a field of `Entry` and not of `Target`, so that `Target`, which
`Resolve` and `Lookup` also return, stays as it is.

## One row for each database

A row stands for a database, not for an object. `dbfwd` reads the objects of
the scope, walks the owner chain of each, as lookup step 2 does
(`docs/LOOKUP.md`), and folds every object into the row of the object at the
top of its chain.

1. **A cluster CR of an operator** is a row. Its Services, StatefulSets,
   Deployments and pods fold into it. The roles come from the rules of the
   operator: `rw` always, and `ro` when the rule for `?role=ro` finds a
   Service. A CloudNativePG cluster with three Services, its pods and its
   Pooler is one row with the roles `rw` and `ro`.
2. **A child that names another database** is a row of its own, owned by the
   cluster. A CloudNativePG `Database` is a row, because
   `k://billing.prod/` opens another database than the cluster's row does.
3. **A child that is another way in** is a row of its own too. A CloudNativePG
   `Pooler` is a row, owned by the cluster, because a user can choose to
   connect through PgBouncer.
4. **A database with no operator** is a row for the StatefulSet or the
   Deployment that runs it, or for a pod that nothing owns. Its Service and
   its pods fold into it. The next section says what counts.

The URL of a row is the shortest that `Resolve` maps to the same database:

1. `k://<name>.<namespace>/` when the name alone reaches it.
2. `k+<operator>://<name>.<namespace>/` when another object in the namespace
   has the same name and `k://` matches it first, or is ambiguous.
   `dbfwd` checks the rows of the listing against the lookup order of
   `docs/LOOKUP.md`, so it needs no extra call.
3. `?context=<name>` when the URL of the listing named a context.

For a database with no operator, the URL can match its Service before its
StatefulSet. Both reach the same database, which is what the URL promises.

The check leaves out lookup step 4, the Helm release, because a listing does
not read releases as such. A Helm release whose name is the name of a row,
and whose Service is another database, is the one case where the URL of a row
can reach another database.

## What counts as a database with no operator

A StatefulSet, a Deployment or a pod that nothing owns is a row only when its
pod template has a sign of a database. Listing every workload and guessing is
noisy, so the sign is one of these, in order:

1. A container image that a pod spec rule of `docs/LOOKUP.md` knows, such as
   `postgres`, `mysql`, `mariadb` or `bitnami/postgresql`.
2. An environment that a pod spec rule knows, such as `POSTGRES_PASSWORD` or
   `MYSQL_ROOT_PASSWORD`.

A port alone is not a sign, because many programs that are not databases
listen on 5432 or 3306. A database that no driver of `usql` reaches, such as
MongoDB or Redis, is no row, because `Resolve` cannot connect to it.

A Helm label, such as `app.kubernetes.io/instance`, names the release in
`Owner` but is not a sign by itself. A workload with no sign is no row. A
`k://` URL still reaches it by its name, as before, because a listing changes
nothing in `Resolve`.

## What a listing reads

A listing never reads a Secret, and never lists Secrets. `Credential` names
where `Resolve` reads the password, from the rule of the operator or the
pod spec, and reads nothing. A row whose credential comes from a Secret that
RBAC hides still lists.

It reads, in each namespace of the scope:

1. The kinds that the cluster has, with `kubectl api-resources`, as lookup
   does. A kind that is not installed is never asked for.
2. With one `kubectl get` and a list of kinds, every cluster CR and child of
   the installed operators, and `services`, `statefulsets`, `deployments` and
   `pods`. With `*` as the namespace, it adds `--all-namespaces`.

That is two calls for a scope of any size. When the one `get` fails because
RBAC forbids a kind, `dbfwd` asks for each kind alone, so that it can list the
kinds it is allowed to see and report the others in `Skipped`. That is one
call for each kind, only in that case.

The first call runs through the terminal wrapper, as the first call of
`Resolve` does, so a credential plugin can prompt. Each call runs with `ctx`,
so canceling it, as `usql` does on Ctrl-C, kills `kubectl`. Each call also
passes `--request-timeout=30s`, so that a cluster that does not answer fails
in 30 seconds and does not hang.

## RBAC

D10 says that `dbfwd` never lists across the cluster. A listing is the
exception, because the user asks for it. `Resolve` and `Lookup` stay as D10
says.

1. A listing of one namespace needs `list` on the kinds it reads, in that
   namespace. A user who can list only one namespace can list that one.
2. A listing of every namespace asks for `--all-namespaces`, which needs
   `list` across the cluster. When RBAC forbids it, `List` fails with
   `ErrForbidden`, and its message says that listing every namespace is not
   allowed. It does not list another scope in its place, because that is not
   what the user asked for. It never lists the namespaces to go through them
   one by one, because that is a cluster-wide `list` too.
3. A kind that RBAC forbids in a namespace is skipped with a note, and the
   others still list. The listing fails only when it reads nothing.

## The command

`cmd/dbfwd` gets `dbfwd list`, which calls the same `List`:

```text
dbfwd list [-A | -n NAMESPACE] [--context NAME] [PATTERN]
```

The flags follow `kubectl`: `-A` is every namespace, `-n` is one namespace,
and `--context` is a context. `PATTERN` is a name with `*`, and the command
builds the URL from the flags, such as `k://pg*.prod?context=x` from
`-n prod --context x pg*`. It prints one row for each entry, then each
`Skipped` part, to standard error.

`usql` can build the URL in the same way for `\k`, so the flags of the two
commands mean the same thing.

## Testing

The unit tests in `k8s/list_test.go` run `List` against the fake `kubectl`,
with a namespace that holds a CloudNativePG cluster with a Pooler and a
Database, a StatefulSet of the official postgres image, a web server, and two
clusters of different operators with the same name. They check each row, the
filters, every namespace, a forbidden kind, and that no call reads a Secret.
`cmd/dbfwd/list_test.go` checks how the command builds the URL from its
flags, and the table it prints.

`dbfwd list` was run by hand against the kind cluster of the suites, on
2026-11-08, and listed the CloudNativePG, mariadb-operator and MOCO fixtures
in about half a second. The URL of a row of a plain StatefulSet resolved to
the same database.

The cluster suites do not call `List` yet. When they do, each suite checks
that the database of its fixture is one row, with a URL that the suite
already resolves.
