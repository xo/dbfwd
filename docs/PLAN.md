# Plan

## Aim

A person connects to a database that runs on Kubernetes, with a URL that holds
no password, such as `k+cnpg://pg.prod/app` or `k://pg-rw.prod/`. `dbfwd`
finds the database, reads its credentials, starts a port-forward, and returns
an ordinary URL for a local port. `usql` calls it, and so does the `dbfwd`
command. A user of `usql` never sees the name `dbfwd`.

## Where it stands

The design is written and decided. No forwarding code exists yet.

| Area | Document | Decisions |
| --- | --- | --- |
| The shape: a resolver, a tool per backend, the `Forward` | this file | D2, D3, D4, D11 |
| The Go API | `docs/API.md`, Proposed | none yet |
| The URL | `docs/URL.md` | D5, D6 |
| How a name becomes a database | `docs/LOOKUP.md` | D7 |
| The operators | `docs/OPERATORS.md` | D8, D9 |
| Credentials and security | `docs/URL.md` and D10 | D10 |
| Testing | `docs/TESTING.md` | D12, D13 |
| The command line tool | `docs/CLI.md` | D14 |

## What other projects give

| Project | What `dbfwd` uses | State |
| --- | --- | --- |
| `dburl` | The `k8s` scheme, its aliases and its parsing | Released in v0.57.0 |
| `dbmeta` | The `dbrun` kind cluster for the tests | Released in v0.34.0 |
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
   session. It closes the `Forward` on `\c` and at exit. When the forward is
   lost, `usql` tells the user and offers to reconnect.
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

1. Ken reviews the API in `docs/API.md`.
2. Write the `k8s` backend against a fake `kubectl`, then against a cluster
   from `dbrun`.
3. Check the owner chains of the CloudNativePG `Pooler` and `Database` and the
   mariadb-operator `MaxScale` in the upstream source, before lookup step 2 is
   written.
4. Write `cmd/dbfwd`. `dbfwd usql` waits for the release of `usql` that reads
   `USQL_DSN`.

## Open questions

Ask Ken. Do not answer one on your own.

The API proposal has three, at the end of `docs/API.md`.
