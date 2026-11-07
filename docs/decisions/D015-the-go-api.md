# D15. The Go API

Status: Decided.

The root package `dbfwd` has the API in `docs/API.md`, and the code follows
it. In short:

1. `Resolve(ctx, u, opts...)` returns a `Forward` once the forward is ready.
   `ctx` stops it only until then, and `Close` stops it after.
2. `Lookup` runs the lookup and reads no credential. `Handles` tells a caller
   whether a backend takes a URL.
3. The options follow the functional options pattern: `WithTool`, `WithEnv`
   and `WithInteractive`. None is tied to one backend.
4. `Forward.URL(ctx)` returns the local URL. A caller calls it for each new
   connection, and a backend with a credential that expires renews it there.
   No field holds the password, and `String`, `GoString` and `LogValue` print
   only the target.
5. The errors are constants of the type `Error`, and `LookupError` lists the
   steps tried.
6. A backend registers itself from `init`, and a caller imports it with a
   blank import, as `database/sql` does: `import _ "github.com/xo/dbfwd/k8s"`.
7. `kubectl` chooses the local port. There is no option for a fixed one.

`usql` reviewed the API and fits it.

Rejected: an `Options` struct, a field that holds the local URL, and a root
package that links every backend.
