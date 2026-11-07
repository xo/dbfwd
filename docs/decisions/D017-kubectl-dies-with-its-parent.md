# D17. kubectl dies with its parent

Status: Decided.

Ken decided on 2026-11-08 that `kubectl` dies with its parent where the
system allows it.

`usql` found that when its process is killed with SIGKILL, so that no
`Close` runs, `kubectl port-forward` and `kubectl get pod --watch` keep
running and hold the local port.

1. On Linux and FreeBSD, `dbfwd` sets `Pdeathsig` to SIGKILL on each
   `kubectl` that outlives the call that starts it: the port-forward and the
   watch. The system kills that `kubectl` when `dbfwd` dies, by any signal.
2. The system sends that signal when the thread that started the child exits,
   not the process. So the child starts from a goroutine that locks its OS
   thread and holds it until the child exits. Each forward costs two OS
   threads.
3. Only Linux and FreeBSD have the field `Pdeathsig`, and AGENTS.md forbids a
   build constraint on a system. So `dbfwd` sets the field by name, through
   `reflect`. On another system it does nothing.
4. On another system, a `kubectl` outlives a `dbfwd` that is killed with no
   `Close`. `Close`, and a canceled `Resolve`, still stop it everywhere.
5. A short `kubectl get` gets no such signal. It ends on its own within its
   timeout.

Rejected:

1. A file with a Linux build constraint, which AGENTS.md forbids.
2. A pipe on the stdin of `kubectl`, which `kubectl` never reads, so it never
   sees the end of the pipe.
3. A process group that `dbfwd` kills, which needs `dbfwd` to run, so it does
   not survive SIGKILL either.
