# D5. The scheme is k8s, and k is the short form

Status: Decided.

A forward URL is `k8s+<operator or type>://...`, and `k+<operator or type>://...`
is the short form. The `k8s` scheme has the aliases `k`, `kc`, `k8`, `kube` and
`kubernetes`. `dburl` v0.57.0 holds them. The part after the `+` is optional.

`dburl` owns the scheme and its aliases. It passes the part after the `+`
through as `Transport`, and leaves `Transport` empty when there is none.
`dbfwd` reads `SchemeName` and `Transport`, and never holds its own list of
the spellings.

Rejected: a bare operator scheme, such as `cnpg://`. It takes a name from the
scheme space of `dburl` for each operator.
