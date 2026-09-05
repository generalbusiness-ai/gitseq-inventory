# Gitseq inventory application

This small JSONata-with-DDL application demonstrates signed inventory events,
a disposable SQLite projection and bounded read-only queries. It uses the
shared `github.com/generalbusiness-ai/tailapps/jsonataddl v0.1.2` core with
repository-local Gitseq host adapters.

**The command accepts only the fixed demonstration below.** Production replay
is refused because deterministic evaluator step and allocation bounds are
still missing. A timeout does not supply those bounds.

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
reserve two, then refuse a reservation for four. The result contains a stock
row with three available units and the verified and interpreted frontier.

`inventory-fixture` never stores the event-signing key and removes the
sequencer private key after the four-record log is complete. Verification
material remains. The query command verifies the binding and signatures and
requires those exact three schemas and canonical payloads, empty causal lists
and the same actor as the binding. Generated keys, IDs and timestamps may vary.
Any other log is rejected before evaluation or database changes. There is no
production override flag.

Use `-sql` to run another bounded read-only query:

```sh
go run ./cmd/jsonata-inventory \
  -repo "$INVENTORY_RUN/events" \
  -database "$INVENTORY_RUN/inventory.sqlite" \
  -sql 'SELECT id, sku, qty FROM reservations ORDER BY id'
```

A matching disposable cache is reused without adding duplicate decisions.
An unrelated database or legacy JSON-affinity database is refused without
migration. An owned cache with a different source identity is rebuilt from the
admitted fixture. The command does not replace old log bindings.

## Application boundary

[`application.sql`](application.sql) declares one private inventory event,
stock and reservation tables, one read-free normalizer, one analytic fold and
two exported reads. [`normalize.jsonata`](folds/normalize.jsonata) projects the
validated record payload and schema into that private event.
[`inventory.jsonata`](folds/inventory.jsonata) applies receipts or reservations
and explicitly selects the reservation columns. Its stock decisions preserve
the earlier Gitseq application's behavior, checked against the old executable.
The original fold and retained reference fixtures carry the [upstream MIT
terms](internal/recordruntime/testdata/LICENSE.gitseq).

Gitseq's public `host` remains pinned to
`v0.0.0-20260827154243-61439ecd86d3` for signature and binding verification.
No active or test package imports its old spike interpreter. The shared core
owns compilation, evaluation and logical values; Inventory owns record
admission, transactions, read authority and projection lifecycle. No database
meaning enters the sequencing kernel.

The binding selects the composed runtime identity:

```text
jsonata-ddl-runtime:sha256:d506811d6e568fc3e4c0f9773d1d0e12949cf6ab3f6bc891d8a1db7bf0aa90cd
```

See the [architecture](docs/reference/architecture.md) for exact module sums,
identity components, storage rules and query limits, and the
[input contract](docs/reference/input-contract.md) for frozen record bytes.

## Verification and limits

Run `go test ./...` for application, adapter and focused compatibility tests.
Run `python3 scripts/verify-corpora.py` from a clean committed checkout with
Go 1.26.7, Node and Python 3.12 or later for the complete [corpus
gate](docs/reference/corpora.md). It runs the released core corpus, the JSONata
reference cases and differential inventory replay twice. It also mutates the
actual receipt upsert in a disposable copy and requires the stock comparison
to fail. CI requires this gate, full tests, race tests, vet and build.

These checks cover the reviewed expressions and frozen cases. They do not
establish whole-language compatibility, deterministic evaluator resource
bounds, a production submission service, historical query activation or a UI.
