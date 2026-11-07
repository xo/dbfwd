# Contributing to dbfwd

Read two things before you change anything.

[`AGENTS.md`](AGENTS.md) holds the rules, the Go conventions and the lint
policy. It is written for an AI coding agent and everything in it applies to a
person. `CLAUDE.md` holds one line that imports it, for Claude Code.

[`docs/decisions/`](docs/decisions/README.md) holds every decision this project
has made, one file each. Read the status of a decision before you rely on it.

Do not decide an open question on your own. [`docs/PLAN.md`](docs/PLAN.md)
holds where the work stands. Ask Ken.

## Before you send a change

```bash
gofmt -l . && go vet ./... && go build ./... && go test -race -count=2 ./...
golangci-lint run ./...
```

`gofmt -l .` must print nothing. Run the tests with `-count=2`, because that is
what CI runs.

The `test` directory is a separate module, and `./...` does not reach it. Its
cluster tests need a kind cluster from `dbrun`. `docs/TESTING.md` holds how to
run them.

## Agent skills

The repository carries two agent skills. A skill is a set of instructions that
a coding agent loads for a task. `simple-english` sets how prose is written,
and `go-pedantry` sets how Go is written.

`skills-lock.json` names the source of each skill. The `skills` command from
npm writes that file. It writes each skill into two folders. Codex and the
other agents read `.agents/skills/<name>`, and Claude Code reads
`.claude/skills/<name>`.

To update a skill, run this in the repository root. The example updates
`simple-english`:

```bash
npx skills@1.7.0 add AminBlg/SimpleEnglish --skill simple-english --agent codex claude-code --copy -y
```

Keep `--copy`. Without it, the command writes a symbolic link. A Windows
checkout writes a symbolic link as a text file, and Claude Code then loads no
skill and says nothing.

`.claude/settings.local.json` holds the Claude Code permissions of one person.
The root `.gitignore` ignores it.

## What the reviewer will ask

Does the change keep the packages that `usql` imports to the standard library
and `dburl`? Only `cmd/dbfwd` can add `dbmeta` and `ox`.

Does a forward stop and release its port when the context ends?

Can a password reach a log, an error, the screen or the arguments of a child
process?

Is a lint finding a real defect or a linter making idiomatic Go worse? Only the
first gets a code change. The second gets a line in `.golangci.yml` with the
reason.
