# D2. dbfwd is a resolver and not a database/sql driver

Status: Decided.

`dbfwd` takes a URL that `dburl` parsed, starts a forward, and returns an
ordinary URL for a local port. The caller opens that URL as usual.

A forward belongs to the whole connection session, not to one pooled
connection. Wrapping `driver.Conn` means forwarding every optional interface.
The driver hooks in `usql` must see the real driver, such as `postgres`, and
never `k8s`.

`dburl` parses the URL and does nothing else. It has no network code, no
subprocess and no dependency, and `dburl.Open` refuses a `k8s` URL.

Rejected: a registered driver named `k8s`.
