# D11. Resolve returns a Forward

Status: Decided.

`Resolve` takes `ctx` first, the parsed URL and options, and returns a
`Forward`. The `Forward` holds:

1. `URL`, a method that returns the local URL, such as
   `postgres://app:...@127.0.0.1:41234/app`. A backend with a credential that
   expires, such as an RDS IAM token, makes a new one there. No field holds
   the password. A caller calls `URL` for each new connection, so `URL` is
   cheap when nothing expired, safe for many goroutines, and fails with
   `ErrForwardLost` once the forward is lost.
2. The resolved user and the database.
3. The target as resolved: the backend, where it lives, the kind that matched,
   its name, what owns it and the database type.
4. `Done` and `Err`, so that a caller can tell that the forward was lost.
5. `Close`, which stops the forward and releases the port.

The options follow the functional options pattern, and none is tied to one
backend. They name the path of a tool, the environment of each child process,
such as `KUBECONFIG`, and the wrapper for a terminal. `docs/API.md` holds the
API.

`dbfwd` prints nothing. Its errors include a kind for "not allowed by RBAC"
apart from "not found".

When a running forward dies, because a pod restarted, a failover happened or a
token expired, `dbfwd` does not start it again. `Done` closes and `Err` says
why, and the caller decides. An open transaction is lost either way, and a
silent restore hides that loss.

A credential plugin in a kubeconfig, such as kubelogin, can prompt on the
terminal, and so can a cloud login. `usql` holds the terminal in raw mode, so
the terminal wrapper runs around each call of a tool that can prompt, and
`usql` passes a function that releases the terminal. The first call of the
tool in `Resolve` runs through it, so the login happens once in the
foreground. The long running tunnel, such as `kubectl port-forward`, runs with
stdin from `/dev/null` and no terminal. If its login needs to prompt later,
the forward fails and `Done` reports it. With no wrapper, no child process has
a terminal, so a login that prompts fails and does not hang.

Rejected: restoring a lost forward on the same port, and a caller-supplied
stdin, stdout and stderr, which cannot take the terminal from the line editor
of `usql`.
