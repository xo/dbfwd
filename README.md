# dbfwd

`dbfwd` connects a program such as [`usql`](https://github.com/xo/usql) to a
database that runs on Kubernetes. It reads a URL such as
`k+cnpg://pg.prod/app` or `k://pg-rw.prod/`, finds the database, starts a
port-forward, and gives back an ordinary URL for a local port. The URL holds
no password: `dbfwd` reads it from the cluster.

`dbfwd` is a Go package, and later a command, `dbfwd`, that runs the native
client of a database or `usql` through the forward. The package has a first
implementation for Kubernetes and has not run against a real cluster yet.
[`docs/PLAN.md`](docs/PLAN.md) says where it stands.

## Documents

| Document | What it holds |
| --- | --- |
| [`docs/PLAN.md`](docs/PLAN.md) | The aim, where the work stands, what other projects give, and the next steps |
| [`docs/API.md`](docs/API.md) | The Go API of the package |
| [`docs/URL.md`](docs/URL.md) | The URL that `dbfwd` reads |
| [`docs/LOOKUP.md`](docs/LOOKUP.md) | How `dbfwd` finds a database from a name, with or without an operator |
| [`docs/OPERATORS.md`](docs/OPERATORS.md) | What each supported operator creates, and where `dbfwd` reads it |
| [`docs/CLI.md`](docs/CLI.md) | The `dbfwd` command, which runs a native client or `usql` through a forward |
| [`docs/TESTING.md`](docs/TESTING.md) | How `dbfwd` is tested, locally and in CI |
| [`docs/decisions/`](docs/decisions/README.md) | Every decision, one file each |
| [`AGENTS.md`](AGENTS.md) | The rules for a coding agent. Every rule applies to a person too |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How to send a change |

## License

MIT. See [`LICENSE`](LICENSE).
