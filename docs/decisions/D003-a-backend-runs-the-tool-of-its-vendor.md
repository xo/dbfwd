# D3. A backend runs the tool of its vendor

Status: Decided.

A backend runs the command line tool of its vendor as a child process, with
`exec` and no shell. It links no SDK of the vendor. The `k8s` backend runs
`kubectl get -o json` and `kubectl port-forward`. A later backend runs its
own tool, such as `aws ssm start-session` or `cloud-sql-proxy`.

A tool brings what a person already set up: the kubeconfig, the contexts, the
credential plugins and the logins of each cloud. It adds no dependency.

Everything `dbfwd` links lands in every `usql` binary. `k8s.io/client-go`
v0.37.2 grew a stripped `usql` from 193.0 MiB to 215.7 MiB, and the typed
client alone saves almost nothing, because `k8s.io/api` registers its types at
init. The SDK of a cloud costs the same kind of size. If a backend ever needs
a library, it is a separate package that the default import does not pull in.

Rejected: an SDK in the default build, and a hand written client for an API.
