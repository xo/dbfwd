# D8. The map from an operator to a scheme lives in dbfwd

Status: Decided.

The map from an operator name, such as `cnpg`, to a database scheme, such as
`postgres`, lives in the `k8s` package, beside the rules of each operator.

`dburl` does not hold the map. It passes the operator name through. `dbmeta`
does not hold it either, because `dbmeta` holds dialect knowledge and not
deployment conventions.

An operator and its Secret names change with the release, so the map and the
rules of each operator change together. One place for both keeps them in step.
