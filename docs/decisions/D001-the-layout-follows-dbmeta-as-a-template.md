# D1. The layout follows dbmeta as a template

Status: Decided.

`dbfwd` uses the layout and the working rules of `dbmeta`: an `AGENTS.md` that
every agent reads, a one line `CLAUDE.md`, a `docs/` folder with one file per
decision, the `go-pedantry` and `simple-english` skills, and a
`golangci-lint` configuration with `default: all`.

The layout is a template and not a copy. Anything that is specific to
`dbmeta`, such as models and containers, stays out. A part of the layout comes
in only when `dbfwd` needs it.

Rejected: copying `dbmeta` whole and deleting what does not apply. That leaves
rules and tests that nobody here can explain.
