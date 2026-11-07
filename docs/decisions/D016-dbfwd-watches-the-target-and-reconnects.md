# D16. dbfwd watches the target and reconnects

Status: Decided. Amends D10 and D11.

Ken decided on 2026-11-07 that `dbfwd` watches what it forwards to, and
reconnects on its own.

1. `dbfwd` always forwards to a pod that it picks itself: the pod by name, or
   a pod that the Service, the Deployment or the role of the operator
   selects. It picks a ready pod first. When no pod is ready, it picks a pod
   that runs, as `kubectl port-forward` does, because an operator can keep a
   pod that serves unready for a while, such as the HAProxy of Percona XtraDB
   Cluster. When no pod runs, such as while an operator replaces it, `Resolve`
   waits up to 20 seconds for one, as `kubectl port-forward` does. It never
   leaves the choice of the pod to `kubectl`.
2. It watches that pod with `kubectl get pod --watch`. When the pod is
   deleted, stops running, stops being ready after it was picked as ready, or
   no longer matches the role it was picked for, such as after a failover
   moves the primary, `dbfwd` picks a pod again by the same rule and starts
   `kubectl port-forward` again on the same local port. The URL that the
   caller holds keeps working.
3. When `kubectl port-forward` stops on its own, `dbfwd` reconnects the same
   way.
4. If no pod runs for two minutes, such as when the cluster was deleted,
   `dbfwd` stops, and `Done` and `Err` report `ErrForwardLost`.

A connection that was open through the old pod breaks, and an open
transaction is lost. Only a new connection reaches the new pod.

The least RBAC of D10 gains `watch` on Pods.

Rejected: reporting a lost forward on its next use only, which leaves `usql`
to notice, and polling the pod with `get`, which reports a change late.
