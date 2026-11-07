# D4. SSH comes after Kubernetes

Status: Decided.

An SSH local forward through a jump host is a future kind of forward. `dbfwd`
builds the Kubernetes backend first, and nothing for SSH until that works.
There is no `ssh` package, no `ssh+` scheme and no SSH dependency.

The root package keeps a registry of backends, so the SSH backend can be added
without a change to the caller.

The choices for SSH are made when its work starts: running `ssh -L`, which
honors `~/.ssh/config` and the agent, or `golang.org/x/crypto/ssh`, which
reads no ssh config, and the URL syntax for a jump host and a target, which is
agreed with `dburl`.
