# D18. dbfwd lists the databases it can reach

Status: Decided. Amends D10.

Ken decided on 2026-11-08 that `dbfwd` lists the databases it can reach, as
`docs/LIST.md` describes.

`usql` asks for a meta command, such as `\k`, that lists the databases in a
cluster with the URL to pass to `\c`. `docs/LIST.md` holds the design.

1. `List(ctx, u, opts...)` takes the same URL as `Resolve`, with `*` in the
   name and the namespace, such as `k://*`, `k://*.prod` or `k://*.*`. It
   returns a `Listing` with the scope it resolved, one `Entry` for each
   database, and a `Skipped` list of what RBAC hid. An `Entry` names the
   operator of the database.
2. A row stands for a database: a cluster CR with everything it owns folded
   in, a child that names another database or another way in, such as a
   CloudNativePG Database or Pooler, or a workload with no operator that
   shows a sign of a database, such as a known image.
3. Each row holds the shortest URL that `Resolve` maps to the same database.
4. A listing reads no Secret. It reports where the password comes from.
5. A listing takes two `kubectl` calls for any scope: `api-resources`, and
   one `get` of every installed kind.
6. A listing of every namespace is the one place where `dbfwd` lists across
   the cluster, because the user asks for it. When RBAC forbids it, the
   listing fails with `ErrForbidden`. This amends D10.
7. `cmd/dbfwd` gets `dbfwd list`, with the flags of `kubectl`, on the same
   function.
8. `ListURL(backend, scope, where...)` builds the URL of a listing from a
   scope that a person typed and the fields that edit it, such as the
   namespace and the context. The backend owns the rules, so the root names
   no Kubernetes term. `dbfwd list` and the `\k` command of `usql` both call
   it. Ken chose this form on 2026-11-08.

Rejected:

1. A scope type of its own, which a caller has to learn beside the URL.
2. An empty host for the current namespace, such as `k://`, which `dburl`
   rejects.
3. A row for each object, which shows a CloudNativePG cluster as more than
   six rows.
4. Listing every workload and guessing, which is noisy and slow.
5. Listing the current namespace when every namespace is forbidden, which
   lists a scope that the user did not ask for.
6. An operator field on `Target`, which `Resolve` and `Lookup` also return.
7. `ScopeURL(scope, all, namespace, context)` in the root package, which
   names Kubernetes terms there.
8. A copy of the scope rules in `usql`, which parses no URL of its own.
