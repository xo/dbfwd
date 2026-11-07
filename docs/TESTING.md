# Testing

This document holds how `dbfwd` is tested: on a local machine against a
temporary cluster that is the same every time, on a local machine against a
cluster that is already there, and in GitHub Actions. D12 holds the layers,
and D13 holds who starts the cluster.

## Four layers

| Layer | Needs | Runs | What it proves |
| --- | --- | --- | --- |
| Unit | Nothing | Every push, every machine | The lookup, the URL rules, the credential rules and the error kinds |
| Replay | Nothing | Every push, every machine | `dbfwd` reads real `kubectl` output from each operator and chart correctly |
| Cluster | A temporary cluster | On demand, and in CI | The forward works, and a real driver connects through it |
| Existing cluster | A cluster you name | On demand only | The same tests, against a cluster that you already run |

The first two layers live in the root module and never start anything, which
follows rule 12 in `AGENTS.md`. The last two live in the `test` module, which
has its own `go.mod`, so the drivers and test tools stay out of every consumer
build.

## Unit tests

`dbfwd` runs `kubectl` through one small interface, defined where it is
consumed. A test replaces it with a fake that returns a fixed answer for each
command. The fake records each command, so a test can check that `dbfwd` asked
for the least it needs, and never listed across namespaces.

The unit tests cover:

1. The URL: every form of the user part, a name with and without a namespace,
   the type present and absent, and the query keys.
2. The order of the lookup, including that the first match wins and that a
   kind that is not installed is skipped (D7).
3. The credentials: `secretKeyRef`, `_FILE` with a Secret volume, `envFrom`,
   and a plain value. The choice of the database container when an exporter
   carries the same variables.
4. The errors: not found, not allowed by RBAC, a type that the signals
   disagree on, and an ExternalName Service. A test checks that no error text
   holds a password.
5. The `Forward`: `Done` and `Err` after the fake forward exits, and `Close`
   after the context is canceled.

## Replay tests

A replay test feeds `dbfwd` the JSON that `kubectl get -o json` printed on a
real cluster. The JSON is kept in `testdata/`, one folder per operator or
chart and release, such as `testdata/cnpg-1.30.1/`. Each folder holds the
cluster CR, the Services, the Pods and the Secrets, and a file that says what
`dbfwd` must find from each name.

The cluster tests record these files. Run them with `-update`, and they
write the JSON of each fixture to `testdata/`. Before writing, the recorder
replaces every value in every Secret with a fixed fake value, and removes
`managedFields`, UIDs and timestamps. A test fails if a file in `testdata/`
holds a Secret value that is not one of the fake values.

This layer is what lets every operator be tested on every push. A cluster
for Couchbase or Oracle is too heavy for every push, but its JSON is not.

## Cluster tests

### The temporary cluster

The cluster is kind, which runs Kubernetes nodes as containers. It runs under
podman on a development machine and under docker in GitHub Actions. kind is
what the Kubernetes project uses for its own tests.

`dbrun`, in `dbmeta`, starts and removes the cluster, locally and in CI (D13).
It starts a bare cluster, records its owner and returns its kubeconfig. The
`dbfwd` test module does everything inside the cluster. CI does not use
`helm/kind-action`. A cluster is not shared between owners, and clusters have
a limit of their own.

The cluster is the same every time:

1. The kind node image is pinned by version and digest.
2. Each operator and chart is pinned by release, and installed from its
   published manifest or chart at that release.
3. Each fixture is a file in the `test` module: a cluster CR, or the values
   for a chart, with a test password that the fixture sets.
4. The test waits for the cluster CR to report ready before it runs.

One test run asks `dbrun` for a cluster, installs one operator, builds its
fixtures, runs the tests, and asks `dbrun` to remove the cluster. A flag keeps
the cluster for debugging.

### The dbrun commands

`dbmeta` v0.34.0 holds these, and its D269 records the design. The CI
workflow checks out a pinned `dbmeta` release, v0.34.0 or later. Name a
cluster release exactly, as `k8s-1.37`, and never the bare `k8s`.

| Command | What it does |
| --- | --- |
| `dbrun list --json --names clusters` | Lists the releases: `k8s-1.34` to `k8s-1.37` |
| `dbrun start k8s-1.37` | Makes the cluster of the caller, or starts it again, and returns when the node is Ready and the API answers |
| `dbrun dsn --json k8s-1.37` | Prints the cluster name, the context and the kubeconfig path |
| `dbrun stop k8s-1.37` | Stops the node and keeps the cluster |
| `dbrun remove k8s-1.37` | Deletes the cluster |
| `dbrun status --json k8s-1.37` | Shows each cluster of that release and its owner |

`dsn --json` prints:

```json
{"name":"k8s-1.37","product":"k8s","release":"1.37","kind":"cluster",
 "image":"kindest/node:v1.37.0@sha256:...","cluster":"k8s-1.37-<owner8>",
 "context":"kind-k8s-1.37-<owner8>",
 "kubeconfig":"/home/ken/.local/share/dbmeta/clusters/k8s-1.37-<owner8>/kubeconfig"}
```

The test module runs every `kubectl` with `--kubeconfig` and `--context` from
that output, and never reads `~/.kube/config`. The name of a cluster ends with
the first eight letters or digits of the owner, so set `DBMETA_OWNER_NAME` on
every `dbrun` command. At most two clusters run at once. In CI, set
`DBMETA_RUNNER=docker`.

The node images are pinned by tag and digest: 1.34.11, 1.35.8, 1.36.4 and
1.37.0. A first start takes about three minutes with the image pull, and a
start of a stopped cluster takes about five seconds.

The pinned versions live as Go data in the `test` module, one entry per
operator or chart, as `dbmeta` keeps its container list. The CI workflow
reads that list, and a test fails when the workflow and the list disagree.

### What a cluster test checks

For each fixture, the test calls `Resolve` with each name that the fixture
answers to: the cluster CR, a Service, the Helm release, the StatefulSet and a
Pod. It then opens the returned URL with the real driver and runs a query,
such as `SELECT 1`. It checks:

1. Each name reaches the same database, as the same user.
2. `?role=ro` reaches a replica, where the operator has one.
3. A `<secret>:<field>` user part and a `<user>` user part both work.
4. Deleting the primary pod makes `Done` report a lost forward.
5. `Close` stops `kubectl` and frees the local port.

### Least RBAC

Each cluster test also runs as a ServiceAccount that has only the least RBAC
of D10, in the one namespace: `get` on each kind that `dbfwd` reads, `list`
on Services, and `create` on `pods/portforward`. The test proves that `dbfwd` needs nothing more. A
second ServiceAccount has no `get` on Secrets, and the test checks that
`Resolve` returns the RBAC error kind, apart from not found.

### The credential plugin

A test kubeconfig names a small exec credential plugin, built by the test,
that prompts on the terminal. The test checks that `Resolve` runs it through
the wrapper the caller supplies, and that `kubectl port-forward` never gets a
terminal (D11). With no wrapper, the plugin fails at once and does not hang.

## Existing cluster

A person who already runs a cluster, such as a shared development cluster,
can run the same cluster tests against it. This is never the default. It
needs two settings:

```bash
cd test && DBFWD_TEST_CONTEXT=<kube context> DBFWD_TEST_NAMESPACE=<namespace> go test ./...
```

The tests then:

1. Use only the context named, and never the current context by default.
2. Work only in the namespace named. The namespace must already exist and
   must carry the label `dbfwd.xo/test=true`, so that a typo cannot point the
   tests at a namespace that holds real data.
3. Install no operator. They test only the operators that are installed, and
   skip the rest with the reason.
4. Delete every object they made when they end, and name every object with a
   prefix of `dbfwd-test-`.

## CI

GitHub Actions on `ubuntu-latest`, as in `dbmeta`.

| Job | When | What |
| --- | --- | --- |
| Unit | Every push and pull request | Lint, vet, the unit tests and the replay tests, with `-race -count=2` |
| Cluster | Every push and pull request | One job per operator in the push list. Each job checks out a pinned `dbmeta` release and asks `dbrun` for its own kind cluster, so the jobs run in parallel and one heavy operator cannot starve another |
| Nightly | Once a night | Every operator and chart, on the oldest and newest Kubernetes release that each one supports |

An operator goes in the push list when its cluster starts in a few minutes on
a standard runner. CloudNativePG, mariadb-operator, MOCO, the Zalando and
Crunchy operators and the Helm charts are likely to. Heavy or licensed ones,
such as Couchbase, ECK, K8ssandra and the Oracle operator, run at night. A
product that needs a license or a registry login is Verified on a development
machine and never in CI, as `dbmeta` does (D118 there).

## The host

The development machine was checked against the rootless guide of kind on
2026-11-07. cgroup v2 is on, systemd 262 delegates `cpu`, `memory` and `pids`
to the user, and podman is rootless. kind needs `cpu`, so no drop-in file is
needed.

kind also asks for four kernel modules: `ip6_tables`, `ip6table_nat`,
`ip_tables` and `iptable_nat`. On this machine, `dbmeta` started a cluster
without them on 2026-11-07, so they are not needed here. Rootless podman
cannot load a module. If a cluster fails on another machine, `dbrun` names
the missing modules, and they are loaded as root:

```bash
printf 'ip6_tables\nip6table_nat\nip_tables\niptable_nat\n' | sudo tee /etc/modules-load.d/iptables.conf
sudo systemctl restart systemd-modules-load.service
```

If kind stops with `could not find a log line that matches`, set
`log_driver = "k8s-file"` under `[containers]` in
`~/.config/containers/containers.conf`.
