# Decisions

Every decision this project made is a file in this folder, one per decision,
named by its number and its title. This table is the index. Find the number
here, then open the file.

Each file opens with its status. "Decided" means Ken chose it. "Proposed"
means it is the working design and Ken has not confirmed it. "Open" means
nobody has chosen yet. A decision that changes an earlier one says so in its
status, as "Amends D1", and the earlier one says it back, as "Amended by D2".
Read the status before the decision.

| No. | Title | Status |
| --- | --- | --- |
| [D1](D001-the-layout-follows-dbmeta-as-a-template.md) | The layout follows dbmeta as a template | Decided |
| [D2](D002-dbfwd-is-a-resolver-and-not-a-database-sql-driver.md) | dbfwd is a resolver and not a database/sql driver | Decided |
| [D3](D003-a-backend-runs-the-tool-of-its-vendor.md) | A backend runs the tool of its vendor | Decided |
| [D4](D004-ssh-comes-after-kubernetes.md) | SSH comes after Kubernetes | Decided |
| [D5](D005-the-scheme-is-k8s-and-k-is-the-short-form.md) | The scheme is k8s, and k is the short form | Decided |
| [D6](D006-the-url-holds-no-password.md) | The URL holds no password | Decided |
| [D7](D007-dbfwd-finds-the-database-from-any-name.md) | dbfwd finds the database from any name | Decided |
| [D8](D008-the-map-from-an-operator-to-a-scheme-lives-in-dbfwd.md) | The map from an operator to a scheme lives in dbfwd | Decided |
| [D9](D009-the-supported-operators.md) | The supported operators | Decided |
| [D10](D010-credentials-stay-with-the-caller.md) | Credentials stay with the caller | Decided, amended by D16 |
| [D11](D011-resolve-returns-a-forward.md) | Resolve returns a Forward | Decided, amended by D16 |
| [D12](D012-tests-replay-recorded-clusters-and-run-on-kind.md) | Tests replay recorded clusters and run on kind | Decided |
| [D13](D013-dbrun-owns-the-kind-cluster.md) | dbrun owns the kind cluster | Decided |
| [D14](D014-the-dbfwd-command-runs-a-native-client-or-usql.md) | The dbfwd command runs a native client or usql | Decided |
| [D15](D015-the-go-api.md) | The Go API | Decided |
| [D16](D016-dbfwd-watches-the-target-and-reconnects.md) | dbfwd watches the target and reconnects | Decided |
| [D17](D017-kubectl-dies-with-its-parent.md) | kubectl dies with its parent | Decided |
| [D18](D018-dbfwd-lists-the-databases-it-can-reach.md) | dbfwd lists the databases it can reach | Proposed |
