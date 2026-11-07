# D7. dbfwd finds the database from any name

Status: Decided.

`k://<name>.<namespace>` makes every attempt it can to connect. `dbfwd` tries
these kinds in order, and the first match wins:

1. A cluster CR of a supported operator.
2. Another resource of an operator that names a cluster, such as a
   CloudNativePG `Pooler` or `Database`.
3. A Service.
4. A Helm release, by the label `app.kubernetes.io/instance`.
5. A StatefulSet or a Deployment.
6. A Pod.

A match that an operator owns uses the rules of that operator. A match with no
operator uses the pod spec: the database container, its environment variables
and the Secrets they reach. `docs/LOOKUP.md` holds every rule, and the
database types that the pod spec rules cover.

Rejected: searching across namespaces, matching a prefix of a name, and
guessing the type from a port alone.
