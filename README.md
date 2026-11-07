<div align="center">
  <a href="#about" title="About">About</a> |
  <a href="#installing" title="Installing">Installing</a> |
  <a href="#using" title="Using">Using</a> |
  <a href="#documents" title="Documents">Documents</a> |
  <a href="#related-projects" title="Related Projects">Related Projects</a> |
  <a href="#contributing" title="Contributing">Contributing</a>
</div>

<br/>

[![Unit Tests][dbfwd-ci-status]][dbfwd-ci]
[![Go Reference][goref-dbfwd-status]][goref-dbfwd]
[![Releases][release-status]][releases]
[![Discord Discussion][discord-status]][discord]

[dbfwd-ci]: https://github.com/xo/dbfwd/actions/workflows/test.yml "Test CI"
[dbfwd-ci-status]: https://github.com/xo/dbfwd/actions/workflows/test.yml/badge.svg "Test CI"
[goref-dbfwd]: https://pkg.go.dev/github.com/xo/dbfwd "Go Reference"
[goref-dbfwd-status]: https://pkg.go.dev/badge/github.com/xo/dbfwd.svg "Go Reference"
[release-status]: https://img.shields.io/github/v/release/xo/dbfwd?display_name=tag "Latest Release"
[releases]: https://github.com/xo/dbfwd/releases "Releases"
[discord]: https://discord.gg/WDWAgXwJqN "Discord Discussion"
[discord-status]: https://img.shields.io/discord/829150509658013727.svg?label=Discord&logo=Discord&colorB=7289da&style=flat-square "Discord Discussion"

# About

`dbfwd` connects a program such as [`usql`][usql] to a database that runs on
Kubernetes. It reads a URL such as `k+cnpg://pg.prod/app` or
`k://pg-rw.prod/`, finds the database, starts a port-forward, and gives back
an ordinary URL for a local port. The URL that the caller writes holds no
password, because `dbfwd` reads the password from the cluster.

`dbfwd` picks the pod it forwards to and watches it. When the pod goes, such
as after a restart or a failover, `dbfwd` forwards to a new pod on the same
local port, and the URL keeps working.

It supports every tier 1 operator in [`docs/OPERATORS.md`](docs/OPERATORS.md),
and a database with no operator, through its Service, StatefulSet,
Deployment, Helm release or pod. It is an early implementation for
Kubernetes. [`docs/PLAN.md`](docs/PLAN.md) says where it stands.

# Installing

The package:

```sh
go get github.com/xo/dbfwd
```

The command, which runs the native client of a database or `usql` through a
forward:

```sh
go install github.com/xo/dbfwd/cmd/dbfwd@latest
```

`dbfwd` runs `kubectl`, so `kubectl` must be on the path, with a kubeconfig
that reaches the cluster.

# Using

Run the client of a database through a forward:

```sh
dbfwd k+cnpg://pg.prod/
```

From Go, `Resolve` returns a `Forward`, which holds the local URL and stops
the forward when it closes:

```go
import (
    "github.com/xo/dbfwd"
    _ "github.com/xo/dbfwd/k8s"
    "github.com/xo/dburl"
)

u, err := dburl.Parse("k+cnpg://pg.prod/app")
if err != nil {
    return err
}
fwd, err := dbfwd.Resolve(ctx, u)
if err != nil {
    return err
}
defer fwd.Close()
local, err := fwd.URL(ctx)
if err != nil {
    return err
}
db, err := sql.Open(local.Driver, local.DSN)
```

[`docs/API.md`](docs/API.md) holds the API, and [`docs/CLI.md`](docs/CLI.md)
holds the command.

# Documents

| Document | What it holds |
| --- | --- |
| [`docs/PLAN.md`](docs/PLAN.md) | The aim, where the work stands, what other projects give, and the next steps |
| [`docs/API.md`](docs/API.md) | The Go API of the package |
| [`docs/URL.md`](docs/URL.md) | The URL that `dbfwd` reads |
| [`docs/LOOKUP.md`](docs/LOOKUP.md) | How `dbfwd` finds a database from a name, with or without an operator |
| [`docs/OPERATORS.md`](docs/OPERATORS.md) | What each supported operator creates, and where `dbfwd` reads it |
| [`docs/CLI.md`](docs/CLI.md) | The `dbfwd` command, which runs a native client or `usql` through a forward |
| [`docs/LIST.md`](docs/LIST.md) | Listing the databases of a cluster, for `dbfwd list` and the `\k` command of `usql` |
| [`docs/TESTING.md`](docs/TESTING.md) | How `dbfwd` is tested, locally and in CI |
| [`docs/decisions/`](docs/decisions/README.md) | Every decision, one file each |
| [`AGENTS.md`](AGENTS.md) | The rules for a coding agent. Every rule applies to a person too |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How to send a change |

# Related Projects

`dbfwd` is one of a set of packages that each do one part of the job, so that
a client can take the parts it needs.

- [`usql`][usql] is a command line client for many databases. It calls
  `dbfwd` for a `k8s` URL and holds the `Forward` for the session.
- [`dburl`][dburl] parses a database URL, including the `k8s` scheme and its
  aliases. `dbfwd` reads the URL that `dburl` parsed, and never parses one.
- [`dbmeta`][dbmeta] reads database metadata. Its `dbrun` starts the kind
  cluster that the tests of `dbfwd` run in, and its client table names the
  native client that the `dbfwd` command runs.

# Contributing

Read [`AGENTS.md`](AGENTS.md) first. It holds the rules, including the ones
that are not obvious: the standard library and `dburl` only in what `usql`
imports, no cgo, no build constraint on an operating system or an
architecture, and no password in a log, an error or the arguments of a child
process.

Run the checks before you send a change, with the same flags CI uses:

```sh
gofmt -l . && go vet ./... && go build ./... && go test -race -count=2 ./...
```

Tests in the root module never need a cluster. They run against a fake
`kubectl`, or replay what a real cluster returned, from `testdata/`. The
cluster tests are in the `test` module, and
[`docs/TESTING.md`](docs/TESTING.md) says how to run them.

# License

MIT. See [`LICENSE`](LICENSE).

<br/>

<div align="center">
  <a href="https://github.com/xo/usql" title="A command line client for many databases">usql</a> |
  <a href="https://github.com/xo/dburl" title="Database connection URLs">dburl</a> |
  <a href="https://github.com/xo/dbmeta" title="Database metadata">dbmeta</a> |
  <a href="https://github.com/xo/dbfwd" title="Forwards to databases on Kubernetes, this project">dbfwd</a> |
  <a href="https://github.com/xo/dbimp" title="Database drivers in pure Go">dbimp</a> |
  <a href="https://github.com/xo/cassandra" title="A database/sql driver for Cassandra">cassandra</a> |
  <a href="https://github.com/xo/dbtpl" title="Go code generated from a database">dbtpl</a> |
  <a href="https://github.com/xo/tblfmt" title="Tables of database results">tblfmt</a> |
  <a href="https://github.com/xo/rline" title="The line editor of usql">rline</a> |
  <a href="https://github.com/xo/transit" title="tree-sitter in pure Go">transit</a>
</div>

[usql]: https://github.com/xo/usql "usql"
[dburl]: https://github.com/xo/dburl "dburl"
[dbmeta]: https://github.com/xo/dbmeta "dbmeta"
