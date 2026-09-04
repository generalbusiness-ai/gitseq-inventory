# Gitseq inventory application

This repository is a small, no-UI JSONata-with-DDL application. Signed Gitseq
events are the source of truth. The command rebuilds a disposable SQLite
projection from a verified event log and runs one bounded, read-only SQL query.

## Run the complete example

You need Git and Go 1.26.7 or later. From this repository:

```sh
INVENTORY_RUN="$(mktemp -d)"
go run ./cmd/inventory-fixture -repo "$INVENTORY_RUN/events"
go run ./cmd/jsonata-inventory \
  -repo "$INVENTORY_RUN/events" \
  -database "$INVENTORY_RUN/inventory.sqlite"
```

The fixture writes three signed example events: receive five units of `ink`,
reserve two, then refuse a reservation for four. The query result reports the
verified and interpreted frontier and a stock row with three available units.
The projection command refuses to overwrite an existing database path.

`inventory-fixture` is a sealed demonstration source: it never stores the
event-signing key and removes the repository's sequencer private key after the
four-record log is complete. The public verification material remains, but the
example log cannot later accept more events. This does not stand in for
production key custody or an event-submission service.

Use `-sql` to select another bounded read-only query:

```sh
go run ./cmd/jsonata-inventory \
  -repo "$INVENTORY_RUN/events" \
  -database "$INVENTORY_RUN/reservations.sqlite" \
  -sql 'SELECT id, sku, qty FROM reservations ORDER BY id'
```

## Application boundary

[`application.sql`](application.sql) and
[`folds/inventory.jsonata`](folds/inventory.jsonata) are carried byte-for-byte
from Gitseq commit `61439ecd86d34f647ea3b1ac6adf0f072bcff343`.
The schema uses only `CREATE TABLE`, primary keys, `TEXT`, `INTEGER`, and
checks. It uses no `ALTER`, trigger, virtual table, attached database, PRAGMA,
extension loading, money, timestamp, or out-of-range JSON integer convention.
The two `CREATE EVENT` and `CREATE FOLD` statements are the focused application
extensions admitted by the reviewed runtime.

The replay/query driver comes from Gitseq commit
`ace6ee14ec864179a5a74c6853170d296b3f15c7`. Its replay and query sequence is
unchanged. The repository integration differs in three explicit ways:

- the module is `github.com/generalbusiness-ai/gitseq-inventory`;
- the application profile loads this repository's embedded SQL and JSONata
  rather than Gitseq's embedded spike fixture; and
- the binding identity and source URL name this standalone application.

The Gitseq runtime dependency is pinned to
`v0.0.0-20260827154243-61439ecd86d3`, the exact reviewed runtime head. No
database meaning or SQLite state enters the sequencing kernel.

The successor shared-core dialect, runtime identity and private host adapters and focused A/B/C corpora are defined in
[`internal/recordruntime`](internal/recordruntime), with its staged delivery
boundary and limits in the [architecture reference](docs/reference/architecture.md).
The existing application binding remains active through this combined adapter
and corpus delivery; I8 owns the migration. Run the [corpus gate](docs/reference/corpora.md)
from a clean committed checkout with Go 1.26.7, Node and Python 3.12 or later.

## Demonstrated and still assumed

The tests demonstrate this exact fold over a verified application-bound log,
two equivalent rebuilds, atomic effective and ineffective judgments, exact
frontier reporting, and refusal of representative writes, PRAGMAs, ambient
functions, and multiple statements on the application-query connection.

The focused corpora cover the published core cases, the existing JSONata
reference cases, and differential inventory replay. They do not establish
whole-language compatibility, deterministic evaluator step or allocation
bounds, all map-order and numeric edge cases, schema
discovery, a production event-submission API, frontier-wait semantics, or a
UI. Its only evaluator assumption is that the expressions used by this
18-line fold retain the behavior exercised by the tests under the pinned Go
JSONata profile. A contrary spike-three result may narrow the SQL or evaluator
surface and require this application to change; it does not justify adding
database concepts to the Gitseq kernel.
