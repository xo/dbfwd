# D10. Credentials stay with the caller

Status: Decided.

`dbfwd` finds a password, such as in a Kubernetes Secret, and gives it to the
caller, and to nothing else. The rules hold for every backend.

1. It never logs or prints a password, a URL that holds one, or the raw
   output of `kubectl` or a Secret. No error text holds a password.
2. It binds a forward to the loopback address only.
3. It runs a tool with `exec` and no shell, and passes every argument as its
   own element. A password never goes in the arguments of a child process.
4. It asks for the least privilege that the backend needs, and each backend
   documents it. For `k8s`, that is RBAC in the one namespace: `get` on each
   kind it reads, `list` on Services for the Helm release step and on Pods
   to find the pod of a Service, and `create` on `pods/portforward`. It never
   lists across the cluster.
