# D14. The dbfwd command runs a native client or usql

Status: Decided.

`cmd/dbfwd` is a command line tool that uses the `dbfwd` package on its own.
It resolves a `k8s` URL, holds the forward, and runs the native client of the
database, or `usql`, against the local port. A person can then test `dbfwd`
on a real cluster without the `dbfwd` support in `usql`. `docs/CLI.md` holds
the commands.

1. The client of each database comes from `container.ClientFor` in `dbmeta`,
   which `dbrun cli` uses too. The family keeps one copy of how to call each
   client.
2. The password goes in an environment variable of the child that the client
   reads, such as `PGPASSWORD`. A client that takes the password only as an
   argument is refused.
3. `dbfwd usql` runs `usql` with the local URL in the environment variable
   `USQL_DSN`, and no URL in the arguments. `dbfwd` never sets `USQL_CONFIG`,
   so the person keeps their own configuration.
4. The command parses its flags with `github.com/xo/ox`.
5. Only the command can import `dbmeta` and `ox`. `usql` never imports it, so
   they add nothing to a `usql` binary. The lint configuration holds this
   rule.

Rejected: a temporary `config.yaml` named by `USQL_CONFIG`, which hides the
configuration of the person for that run, and a separate `go.mod` for the
command, which needs a tag with a prefix for each release for no gain.
