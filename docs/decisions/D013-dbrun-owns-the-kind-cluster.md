# D13. dbrun owns the kind cluster

Status: Decided.

`dbrun`, in `dbmeta`, starts and removes the kind cluster that the cluster
tests use, locally and in CI. `dbmeta` v0.34.0 holds it.

1. `dbrun` starts a bare kind cluster from a pinned node image, records its
   owner, and returns its kubeconfig and context. It installs nothing.
2. The `dbfwd` test module installs each operator, builds the fixtures, and
   runs the tests in that cluster.
3. CI calls `dbrun` too, and does not use `helm/kind-action`. The workflow
   checks out a pinned `dbmeta` release.

A cluster is not shared between owners, because each test run installs
operators and their CRDs across the whole cluster. At most two clusters run at
once, apart from the eight servers of `dbrun`.

The first rule of `dbrun` says that nothing else starts a container on a
shared machine. A kind cluster is containers, so this keeps that rule.

Rejected: the `dbfwd` test module starting kind itself, which goes around the
owner and the limit of `dbrun`. Also rejected: `dbrun` only reserving a slot
while `dbfwd` makes the cluster, which splits the cleanup between two tools.
