# D6. The URL holds no password

Status: Decided.

A forward URL never holds a password, for any backend. The backend finds the
credential itself.

Each backend owns the meaning of its host, its user part and its query keys,
and documents them. A later backend has its own, such as an instance and a
region. One query key means the same for every backend: `role`, which is `rw`
for the primary and `ro` for a replica.

The `k8s` URL is
`k[+<operator or type>]://[<user part>@]<name>[.<namespace>][:<port>]/[<dbname>][?<query>]`.
`docs/URL.md` holds every part.

1. The host splits at its last dot. A namespace is a DNS label and holds no
   dot. A name can hold one, and then the namespace must be given too.
2. With no user part, `dbfwd` uses the default user that it finds.
3. A user part with no colon is a database user. `dbfwd` finds that user and
   its Secret.
4. A user part with a colon is `<secret>:<field>`: the Secret in the
   namespace, and the key that holds the password. `<secret>:` reads the key
   `password`.
5. The form of the user part alone decides its meaning. `dbfwd` never checks
   which of a Secret or a user exists, so a new Secret cannot change what a
   URL means.
6. With no database, `dbfwd` uses the default that it finds.
7. The `k8s` backend owns two query keys, `context` and `role`, with no
   prefix. It takes them out of the query and passes every other key to the
   driver. If a driver ever has a key of the same name, the backend documents
   the clash and stops with an error.
8. The kube context is in `context`, because real context names hold colons,
   slashes, `@` and dots.
9. A Service name can imply a role, such as CloudNativePG `pg-rw`. If `role`
   disagrees with it, `Resolve` stops with an error.

Rejected: a context in the host, deciding the meaning of the user part by a
lookup, a prefix such as `k8s.` on the query keys, and letting `role` win over
the Service name.
