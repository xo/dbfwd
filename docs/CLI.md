# The dbfwd command

This document holds the design of `cmd/dbfwd`, a command line tool that uses
the `dbfwd` package on its own. It resolves a `k8s` URL, holds the forward,
and runs the native client of the database, or `usql`, against the local
port. A person can then test `dbfwd` on a real cluster without the `dbfwd`
support in `usql`. D14 holds the decision.

The command parses its flags with `github.com/xo/ox`. It can import `dbmeta`
and `ox`, and the packages that `usql` imports cannot.

Arguments for the client go after `--`, as in
`dbfwd k+cnpg://pg.prod/ -- -c 'select 1'`. Without `--`, `dbfwd` reads a
flag of the client as its own.

## Commands

| Command | What it does |
| --- | --- |
| `dbfwd <url> [-- <args>]` | Resolves the URL, starts the forward, and runs the native client of the database on the terminal. The arguments after `--` go to the client. When the client exits, `dbfwd` closes the forward and exits with the status of the client |
| `dbfwd usql <url> [-- <args>]` | The same, with `usql` in place of the native client |
| `dbfwd forward <url>` | Starts the forward and holds it until Ctrl-C. It prints the local address, the user and the database, and never the password |
| `dbfwd lookup <url>` | Runs the lookup and prints each step and what matched (D7). It reads no Secret and starts no forward, so a person can see why a name did not match |
| `dbfwd clients` | Lists the client of each database type, and `usql`, and whether each is on the `PATH` |

Flags:

| Flag | What it does |
| --- | --- |
| `--client <command>` | Runs another client of the same database, such as `pgcli` in place of `psql`. It takes the same arguments. Only `dbfwd <url>` has it |
| `--print` | Prints the client command and the names of its environment variables, with the password masked, and runs nothing. Only `dbfwd <url>` has it |
| `--kubeconfig <path>` | Uses that kubeconfig in place of `KUBECONFIG`. It is `WithEnv("KUBECONFIG", path)` |
| `--kubectl <path>` | Runs that `kubectl`. It is `WithTool("kubectl", path)` |

The URL names the context and the role, as `?context=` and `?role=` (D6).
Each flag becomes an option of `Resolve` (D11, `docs/API.md`).

## The password

The password is real. It comes from a Secret in a cluster, and it is not the
throwaway password of a test container. So:

1. `dbfwd` passes the password in an environment variable of the client where
   the client reads one, such as `PGPASSWORD`, `MYSQL_PWD` and
   `SQLCMDPASSWORD`. It never puts the password in an argument, because a
   process list shows every argument to every user of the machine.
2. If a client takes the password only as an argument, such as `sqlplus`,
   `cqlsh` and `mongosh`, `dbfwd` refuses to run it. The error names `dbfwd
   usql` and `dbfwd forward` as the other ways in.
3. `dbfwd forward` and `--print` never print the password.

An environment variable is safer than an argument. Only the same user and
root can read `/proc/<pid>/environ`, and every user can read the arguments.

## Passing the password to usql

`dbfwd usql` runs `usql` with the local URL in the environment variable
`USQL_DSN`, and passes no URL in the arguments. `usql` reads the URL from
that variable. `dbfwd` never sets or replaces `USQL_CONFIG`, so the person
keeps their own configuration and named connections.

The rules of `USQL_DSN`:

1. `dbfwd` sets it in the environment of the child `usql` only, and never in
   its own.
2. It holds the full local URL. `dbfwd` encodes the user and the password for
   a URL, because they come from a Secret.
3. `usql` reads it only when the arguments name no URL. So `dbfwd` passes no
   URL through to `usql`, and refuses one after `--`. Other flags, such as
   `-c` and `-f`, pass through.
4. `usql` reads it once at start and removes it from its own environment, so
   a shell, the pager and the editor never get it. `\conninfo` and the prompt
   mask the password.
5. `dbfwd` never sets `USQL_PASSWORD`. That variable belongs to `-W`, which
   forces a password prompt.

`USQL_DSN` is new in `usql`, and `dbfwd usql` needs a release of `usql` that
reads it.

## The client of each database

`dbmeta` already holds a native client for each product, in its `container`
package: the command, and a function from the host, the port, the user, the
password and the database to the arguments and the environment. `dbrun cli`
uses it, and it already puts the password in an environment variable where
the client reads one. `container` is in the root module of `dbmeta`, which
depends on the standard library alone.

`cmd/dbfwd` uses that table, so the family keeps one copy of how to call each
client. `dbfwd` asks for the client of the type it resolved, such as
`postgres`, with `container.ClientFor`. The clients include `psql`, `mysql`,
`mariadb`, `sqlcmd`, `clickhouse-client`, `trino`, `vsql` and `bendsql`. A
database whose client is missing or not usable, such as Elasticsearch, gets
`dbfwd usql`.

`container.ClientFor(name)` takes a product, such as `mariadb`, or a `dburl`
dialect, such as `postgres`, and returns the client and true. A product wins
over a dialect, so `mysql` gives `mysql` and `mariadb` gives `mariadb`. It
returns false when nothing has the name. `dbfwd` uses a client as follows:

1. If `Command` is empty, the product has no client, and `Why` says why.
   `dbfwd` prints the reason and names `dbfwd usql`.
2. `dbfwd` builds a `Connection` from the local host and port, the user, the
   password and the database, and calls `Call`.
3. If `Call.InArgs` is true, the client takes the password only as an
   argument, and `dbfwd` refuses it. `cqlsh` for Cassandra is one.
4. Otherwise `dbfwd` runs `Call.Command` with `Call.Args`, and adds
   `Call.Env` to the environment of the child only.

InfluxDB 1 and 2 use `influx`, and InfluxDB 3 uses `influxdb3`.
`ClientFor` returns the client of the newest release, so `--client` picks
`influx` for an older one.

`ClientFor` is in `dbmeta` v0.35.0, and `dbfwd` needs that release or a later
one.

## The terminal

`dbfwd` owns the terminal until it starts the client. A credential plugin in
the kubeconfig that prompts, such as kubelogin, prompts then, before the
client starts (D11).

The client runs in the foreground, attached to the terminal. Ctrl-C goes to
the client and not to `dbfwd`. `dbfwd` waits for the client, then closes the
forward. If the forward is lost while the client runs, `dbfwd` prints one
line to standard error that says so, and leaves the client to report its own
connection error (D11).

## Testing

The cluster tests run `dbfwd` against each fixture: `dbfwd <url> -- -c
'select 1'` for `psql`, and the same for each client in the cluster. `--print`
lets a unit test check the call with no cluster and no client.
