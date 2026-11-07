# dbfwd

`dbfwd` connects a program such as `usql` to a database that runs on
Kubernetes. A caller gives it a parsed `dburl` URL such as
`k+cnpg://pg.prod/app` or `k://pg-rw.prod/`, which holds no password. `dbfwd`
finds the database, reads its credentials, starts a `kubectl port-forward`,
and returns an ordinary URL for a local port, such as
`postgres://app:...@127.0.0.1:41234/app`, inside a `Forward` that stops the
forward when it closes.

`dbfwd` is a resolver and not a `database/sql` driver (D2). `dburl` parses the
URL and does nothing else. `usql` calls `dbfwd` when the parsed URL has the
`k8s` scheme, opens the returned URL through `dburl`, and holds the `Forward`
for the life of the session. Kubernetes is the first kind of forward, and SSH
comes after it (D4).

`docs/PLAN.md` holds where the work stands, what other projects give, the
backends that come later, and the next steps.

## Standing rules

These hold in every `xo` repository, for every coding agent.

1. Stage changes for review. Commit and push only when Ken says so.
2. Load the `simple-english` skill before you write any text that a person
   reads: a document, a code comment, an error message or a commit message.
   Follow it for that text.
3. Load the `go-pedantry` skill before you write or review Go code. Follow it
   where it does not conflict with a rule in this file. A rule here wins.

`CLAUDE.md` holds one line that imports this file, so that Claude Code and
every other agent read the same rules. Edit this file, not that one.

## Which document to read

| If you are | Read |
| --- | --- |
| deciding what to build | `docs/PLAN.md`, which holds the open questions. Ask Ken. Do not answer one on your own |
| working on `Resolve` or the `Forward` | `docs/API.md`, then D2, D3 and D11 |
| working on the URL | `docs/URL.md`, then D5 and D6 |
| working on how a name becomes a database | `docs/LOOKUP.md`, then D7 |
| adding or changing an operator | `docs/OPERATORS.md`, then D8 and D9. Read the upstream source at a named release. A model answer is not a source |
| working on credentials or RBAC | D10 |
| working on the command line tool | `docs/CLI.md`, then D14 |
| writing or running a test | `docs/TESTING.md`, then D12 and D13. `dbrun` in `dbmeta` starts the kind cluster |
| asking why something is the way it is | `docs/decisions/README.md`, the index of every decision |
| recording a decision | the rules below, under Writing documentation |
| answering a lint finding | the rule below, under Linting |
| writing a document, a code comment, an error message or a commit message | the `simple-english` skill. Load it first |
| writing or reviewing Go code | the `go-pedantry` skill. Load it first |
| adding or updating an agent skill | `CONTRIBUTING.md`, under Agent skills |

`CONTRIBUTING.md` is the same thing for a person, and shorter.

A document that is not in that table does not exist. If you cannot find where
something is written down, it is not written down, and it is an open question.

## Hard rules

1. Everything `usql` imports from `dbfwd` lands in every `usql` binary. So the
   root package and `k8s` import the standard library and
   `github.com/xo/dburl`, and nothing else. `cmd/dbfwd`, which `usql` never
   imports, can also import `github.com/xo/dbmeta` and `github.com/xo/ox`
   (D14). The `test` module can import what its tests need. Do not add any
   other package without asking Ken first. A backend runs the tool of its
   vendor and links no SDK. A Kubernetes client library adds about 23 MiB to
   a stripped `usql`, and the SDK of a cloud costs the same kind of size. If
   one ever comes, it is a separate package that the default import does not
   pull in (D3).
2. Never write a scheme list, an alias list, or a connection string parser.
   That taxonomy belongs to `dburl`, and `dbfwd` reads the URL that `dburl`
   parsed. The map from an operator to a scheme (D8) names operators, not
   schemes, and is not such a list.
3. Configuration is a value that the caller owns. Do not put a configured
   value in a package level variable.
4. Do not import `usql`. The dependency runs the other way.
5. Every function that talks to a cluster or to the network takes
   `ctx context.Context` as its first parameter. Never call
   `context.Background()` or `context.TODO()` inside the library.
6. Canceling a context must stop a forward and release its local port and
   every connection it holds. A forward that outlives its context is a fault.
7. A password goes to the caller and nowhere else. Never write a password, a
   URL that holds one, or the raw output of a tool or a Secret to a log, an
   error message, a file or the screen. The library prints nothing, and no
   exported field holds a password (D10, D11).
8. Bind a forward to the loopback address only. Run a child process with
   `exec` and no shell, and pass every argument as its own element. Never put
   a password in the arguments of a child process (D10, D14).
9. The root module is pure Go and has no cgo. A consumer builds it with
   `CGO_ENABLED=0` and cross compiles it.
10. Never write a `//go:build` constraint on an operating system or an
    architecture. Testing is `linux/amd64` only.
11. Write idiomatic Go.
12. A test in the root module never needs a cluster. It runs against a fake
    `kubectl` or against recorded JSON in `testdata/`. A test that needs a
    real cluster goes in the `test` module, which has its own `go.mod`, so
    that its dependencies stay out of every consumer build (D12).

## Layout

- `/` is the root package `dbfwd`. It holds `Resolve`, the `Forward` and the
  registry of backends. External projects use this package. It names nothing
  that only one backend has.
- Each backend is a package of its own that registers itself from `init`.
  `docs/PLAN.md` lists the later ones. A new one comes only when Ken chooses
  it.
- `k8s/` is the Kubernetes backend. The rules of each operator live here, and
  so does the map from an operator name to a database scheme, such as `cnpg`
  to `postgres` (D8).
- `cmd/dbfwd/` is the command line tool. It runs the native client of a
  database, or `usql`, through a forward. See `docs/CLI.md`.
- `testdata/` holds the recorded JSON of each operator and chart, for the
  replay tests.
- `test/` is a separate module with its own `go.mod`. It holds the cluster
  tests, which run in a kind cluster that `dbrun` starts.
- `docs/` holds every document except the four that belong in the root.
- `docs/decisions/` holds every decision, one file each.

Do not add a directory that this list does not name.

## Go conventions

Wrap every error with `%w`, never `%s` or `%v`:

```go
if err != nil {
    return nil, fmt.Errorf("finding pod for %s: %w", name, err)
}
```

Write error messages in lower case, starting with a gerund. Do not write
"failed to" or "error". Name the object that failed.

Declare sentinel errors as constants of a defined string type, never as
variables. A `var` declared with `errors.New` can be reassigned by any
importer. A constant cannot. `error.go` shows the form. Keep the `Err` prefix
and the PascalCase name. Compare with `errors.Is`.

Accept interfaces and return concrete structs. Keep an interface to three
methods or fewer. Define an interface where it is consumed, not where it is
implemented.

Use generics rather than `interface{}` for a container of one type. Compare
errors with `errors.Is` and `errors.As`, never by string.

Put `context.Context` first in every parameter list and name it `ctx`. Never
store it in a struct.

Name a package with one short lower case word. Do not stutter.

Name a receiver with one or two lower case letters, and use the same name on
every method of the type. Never write `this` or `self`.

Group struct fields by purpose, with exported fields first and a blank line
between groups.

## Linting

`.golangci.yml` holds the rules.

```bash
golangci-lint run ./...
```

The posture is `default: all` with a disable list. A new linter gets a look
rather than silence.

A linter that makes idiomatic Go worse is disabled, with the reason written
beside it in the configuration. Only a real defect gets a code change. Ask
which one it is before you touch the code. If the remedy reads worse than what
it replaces, that is the answer.

A change made to quiet a linter rather than to fix something is itself a
defect. It leaves code that no reader can explain and that the next person
copies.

Never write `defer func() { _ = rows.Close() }()`. Write `defer rows.Close()`.
A deferred `Close` returns an error nothing can act on. `errcheck` allows it.

Never add an empty `case X:` to satisfy `exhaustive`. `exhaustive` is
disabled.

Read `golangci-lint run` output as a list of questions, not a list of tasks.

## Before you commit

Run these:

```bash
gofmt -l . && go vet ./... && go build ./... && go test -race -count=2 ./...
golangci-lint run ./...
```

`gofmt -l .` must print nothing.

Run the tests with `-count=2`, because that is what CI runs. A test body that
registers something in a package level registry fails the second time.
Registration belongs in `init`.

CI runs on GitHub Actions, on `ubuntu-latest` only. Keep the workflow small.

## Writing documentation

A new document goes in `docs/`. Only `README.md`, `AGENTS.md`, `CLAUDE.md` and
`CONTRIBUTING.md` belong in the repository root. Add a new document to the
table at the top of this file, because a document nobody can find is a
document nobody reads.

A decision goes in a file of its own in `docs/decisions/` and nowhere else.
Name it `D<nnn>-<title>.md` with the next number. It opens with
`# D<n>. <title>`, a blank line, and `Status: <status>.`, where the status is
`Decided`, `Proposed`, `Open`, `Amends D2`, or `Superseded by D4`. Add its row
to `docs/decisions/README.md`. If your decision changes an earlier one, say so
in both statuses, because a reader who finds the older one has to be told.

A status of `Decided` means Ken chose it. An agent that suggests a decision
writes `Proposed`.

Write plain English. Use short sentences and the active voice. Use `can`,
`will` and `must`, and never `should`, `would`, `may`, `might` or `could`. Do
not write a semicolon or an em-dash. Define a term the first time you use it.
The `simple-english` skill holds the full rules.
