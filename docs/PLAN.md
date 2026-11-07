# Plan

## Aim

A person connects to a database that runs on Kubernetes, with a URL that holds
no password, such as `k+cnpg://pg.prod/app` or `k://pg-rw.prod/`. `dbfwd`
finds the database, reads its credentials, starts a port-forward, and returns
an ordinary URL for a local port. `usql` calls it, and so does the `dbfwd`
command. A user of `usql` never sees the name `dbfwd`.

## Where it stands

The design is decided, and the implementation covers every operator of tier
1 (D9):

1. The root package: `Resolve`, `Lookup`, `Handles`, the options, the
   `Forward`, the registry and the errors.
2. The `k8s` backend: the URL, the `kubectl` runner and port-forward, the
   lookup chain, the rules of every supported operator, the pod spec rules
   for a database with no operator, and the watch that reconnects a forward
   (D16).
3. The command `cmd/dbfwd`.
4. Unit tests against a fake `kubectl`, replay tests of recorded clusters, and
   a cluster suite for each operator and for databases with no operator, in
   the `test` module (D12, `docs/TESTING.md`).

## What other projects give

| Project | What `dbfwd` uses | State |
| --- | --- | --- |
| `dburl` | The `k8s` scheme, its aliases and its parsing | Released in v0.57.0 |
| `dbmeta` | The `dbrun` kind cluster for the tests | Released in v0.35.2 |
| `dbmeta` | `container.ClientFor`, the client table for the command | Released in v0.35.0 |
| `usql` | `USQL_DSN`, for `dbfwd usql` | Written, not yet released |

## Shape

1. `dbfwd` is not a `database/sql` driver (D2). A forward belongs to the whole
   session, not to one pooled connection, and the driver hooks in `usql` must
   see the real driver.
2. The core is one call, `Resolve`. It takes `ctx`, the parsed URL and
   options, finds the database, starts the forward, and returns a `Forward`
   (D11).
3. `usql` calls `Resolve` when the parsed URL has the `k8s` scheme, opens the
   returned URL through `dburl`, and holds the `Forward` for the life of the
   session. It closes the `Forward` on `\c` and at exit. When the pod goes,
   `dbfwd` reconnects on the same local port (D16). When the forward is lost
   for good, `usql` tells the user and offers to reconnect.
4. The root package holds the resolver and a registry of backends. `k8s` is
   the first backend, and SSH comes after it works (D4).
5. A backend runs the tool of its vendor as a child process (D3). The `k8s`
   backend runs `kubectl`, which reads `KUBECONFIG` and its current context,
   or the context in the URL. A caller can set `KUBECONFIG` for the call with
   an option. The backend waits until the forward is ready before it returns.

## Future backends

Kubernetes is the first backend. These come later, each as a package of its
own that registers itself, and none is built now. Each one runs the command
line tool of its vendor, as `k8s` runs `kubectl`, and links no SDK (D3).

| Backend | Tool | What it reaches |
| --- | --- | --- |
| SSH | `ssh -L`, or `golang.org/x/crypto/ssh` | A host behind a jump host (D4) |
| AWS Systems Manager | `aws ssm start-session` with `AWS-StartPortForwardingSessionToRemoteHost` | RDS and Aurora in a private VPC, through an EC2 instance, with no SSH |
| Cloud SQL and AlloyDB | `cloud-sql-proxy`, `alloydb-auth-proxy` | A GCP database, with IAM login in the proxy |
| GCP Identity-Aware Proxy | `gcloud compute start-iap-tunnel` | A VM or a private address on GCP |
| Azure Bastion | `az network bastion tunnel` | A database behind Azure Bastion |
| Teleport | `tsh proxy db` | A database behind Teleport |
| HashiCorp Boundary | `boundary connect` | A database behind Boundary |
| Cloudflare Access | `cloudflared access tcp` | A database behind Cloudflare Access |

The likely order after SSH is AWS Systems Manager, then Cloud SQL, then
Teleport. A VPN such as Tailscale needs no backend, because it makes the host
reachable. A hosted database with a public endpoint, such as Neon or
PlanetScale, needs none either.

A backend does two things: it opens a path to the host, and it finds a
credential. Kubernetes, Cloud SQL and Teleport do both. Others mix them: RDS
through Systems Manager with an IAM token from
`aws rds generate-db-auth-token`, or any path with a dynamic credential from
HashiCorp Vault, AWS Secrets Manager or GCP Secret Manager. The credential
source can become a part of its own later, so that the rule that a URL holds
no password (D6) reaches those too. The API leaves room for that in three
ways:

1. A credential can expire. An RDS IAM token lasts 15 minutes. So
   `Forward.URL` is a method that returns the current URL, and a backend with
   a credential that expires makes a new one there (D11).
2. The options of a call name a tool and its environment, and no option is
   tied to one backend (D11).
3. The query keys, the user part and the least privilege belong to each
   backend (D6, D10).

Each new backend needs a scheme in `dburl` first, as `k8s` did, and its own
fake tool and recorded output for the tests (D12).

## Next steps

1. Read the owner chain of the mariadb-operator `MaxScale` in the upstream
   source, then add it to lookup step 2.
2. Run the MySQL Operator suite where `container-registry.oracle.com` serves
   its images. It skips on a machine where that registry stalls.
3. Give the drivers of `usql` a way to accept the certificate of a TLS endpoint
   through a forward, such as that of ECK, whose name is not `127.0.0.1`. This
   is work in `dbimp`.
4. Tier 3 databases: add the pod spec rules of each in `docs/LOOKUP.md`.

## Open questions

Ask Ken. Do not answer one on your own.

1. Listing the databases of a cluster, for a `usql` command such as `\k`
   (D18, `docs/LIST.md`). Ken decides the name and the conventions after
   `usql` and the flagship models have read the design.

