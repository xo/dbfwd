# D12. Tests replay recorded clusters and run on kind

Status: Decided.

`dbfwd` is tested in four layers. `docs/TESTING.md` holds every rule.

1. Unit tests replace `kubectl` with a fake. They need nothing.
2. Replay tests feed `dbfwd` the JSON of a real cluster, kept in `testdata/`
   with every Secret value replaced. They need nothing, so every operator is
   tested on every push.
3. Cluster tests run in a kind cluster that `dbrun` starts (D13). They install
   one operator at a pinned release, build fixtures, and connect through the
   forward with a real driver. They also record the replay files, and prove
   the least RBAC and the behavior of a credential plugin.
4. The same cluster tests run against an existing cluster only when a person
   names its context and a namespace that carries a test label.

The first two layers are in the root module. The last two are in the `test`
module, which has its own `go.mod`.

Rejected: a mock of the Kubernetes API, because `dbfwd` talks to `kubectl` and
not to the API. Also rejected: one shared cluster for every operator in CI,
because one heavy operator then starves the others.
