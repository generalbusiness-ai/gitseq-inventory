# Inventory architecture

The inventory application runs outside the Gitseq sequencing kernel. Gitseq's
public `host` package verifies signed records and application bindings. SQL,
JSONata and disposable SQLite projections belong to this application.

## Current delivery boundary

The public application loader and commands still use the pinned Gitseq spike
runtime. Their binding remains `jsonata-v206-sqlite-spike@0`.
`internal/recordruntime` supplies the successor dialect, identity and host
adapters. Its loader, replay, continuation and query entry points are private,
with only in-package fixture callers. No command uses them yet.

This implements I2, the I3 adapter boundary and the I4 corpus gate of the adopted design
`notes/2026-09-04-tailapps-jsonataddl-adoption.md` at Gitseq commit
`860ee61a07aa753dcbc2d50e74da2b7b6547625b`, sections 2.3–2.8 and 5. I8 switches the application only after this combined delivery passes review.

## Layers and contracts

| Layer | Owner and boundary |
|---|---|
| Verified records and binding (Gitseq layer 4) | `github.com/generalbusiness-ai/gitseq/host`; signed record bytes, ordering and binding selection remain unchanged. |
| Application interpretation (Gitseq layer 5) | Inventory's internal `recordruntime` owns verified-record conversion, read execution, mutation transactions and disposable projections. The external `jsonataddl` core owns compilation, evaluation, validated results, read plans and logical values. The active interpreter is unchanged. |
| Application surface (Gitseq layer 6) | Existing fixture and query commands; no successor command, service or production activation in I3. |

The shared core is exactly
`github.com/generalbusiness-ai/tailapps/jsonataddl v0.1.2`:

```text
Sum: h1:yWVB9oLiPV5pPt3zoPuxUe6nNKx53WIqFWswYOkgOYM=
GoModSum: h1:fpGrE/1ODSULhyxThhr3TL4JtpfcX3PdA4kJBnKWJRY=
```

Its two direct dependencies remain `github.com/jsonata-go/jsonata`
`v0.0.0-20250709164031-599f35f32e5f` and `github.com/ncruces/go-sqlite3`
`v0.35.3`. The core is Apache-2.0 licensed with LICENSE and NOTICE shipped in
the module. No upstream core implementation or conformance corpus is copied here, and there is no
dependency on the Tailapps root module. The old Gitseq spike dependency remains
until the reviewed application migration.

Adding the core selects `golang.org/x/text v0.41.0` instead of `v0.40.0` in
the module graph. It is used by SQLite's dependency tests, not inventory's
compiled runtime packages. The two required runtime pins above do not move.

## Dialect and limits

`GitseqRecord()` returns a fresh `gitseq-record/1` dialect. Sources are
`application.sql` and `folds/*.jsonata`. The host event is `gitseq_record`;
the six non-null TEXT envelope fields are `id`, `schema`, `actor`, `position`,
`timestamp` and `payload_digest`. Causal arrays and decoded payload objects
are normalizer input, not scalar read parameters.

Exactly one normalizer consumes host events and emits the sole private event,
`inventory_event`. At least one analytic fold consumes that event; folds may
not emit events. Every table has one writer. Normalizers may read their own
tables; folds may read their own and normalizer tables. The core enforces this
policy and supplies the default-deny authorizer installed during each analytic
read plan. The host narrows the application to a read-free, write-free
normalizer and exactly one analytic fold.

| Bound | Value | Reason |
|---|---:|---|
| Each source element | 16 KiB | Room for the small inventory DDL and programs. |
| All sources | 64 KiB | Limits aggregate compile input independently of file count. |
| Each program | 16 KiB | Same ceiling as a source element. |
| Evaluation input | 32 KiB | Envelope, bounded payload and a small read result. |
| Evaluation output | 16 KiB | Small event or mutation result. |
| Evaluator depth | 16 | Shallow record and row expressions. |
| Range | 64 | Conservative fixed dialect bound; it grants no range syntax. |
| Emitted events | 1 | One verified record must not yield multiple decisions. |
| Facts | 8 | Allows a short diagnostic list. |
| Row changes | 8 | Inventory requires at most two; leaves a small fixed margin. |
| MANY read rows | 64 | Finite future read ceiling; inventory currently needs one row. |

All eleven values are smaller than the upstream Tailapp dialect's defaults.
These are source/value bounds, **not deterministic evaluator step or allocation
bounds**. The successor runtime remains fixture-only until both missing bounds
exist. A wall-clock timeout is a retryable machine failure, never evidence that
production interpretation is safe. I3 exposes no public successor replay entry point.

## Host adapters and storage

The input adapter recognizes only `stock_received` and
`reservation_requested`. It skips other schemas before payload decoding.
Recognized payloads must fit the 8 KiB ceiling, use canonical JSON bytes and
have exactly the declared `id`, `sku` and positive integer `qty` fields.
It then constructs the unchanged eight-member record envelope. An admitted
record must produce exactly one normalizer emission; emitting none fails.
This explicitly corrects the adopted design's claim that normalizer column
projection alone validates the original payload. The correction versions the
host canonicalization component as `/2`, under inventory request
`3761e1f40afc1fbe71c2007d8eccb1ea781a376b`.

The read adapter binds
declared event parameters, enforces ONE, OPTIONAL ONE and bounded MANY
cardinalities, and uses the core's logical read values. It seats the core's
read authorizer only for that read plan and clears it on every return path.
Host writes occur after the seat is cleared; evaluated programs cannot submit
SQL or choose undeclared mutation tables.

The mutation adapter applies validated inserts, upserts and deletes in a
stable table order. Each verified record has one transaction containing its
application changes, decision, ordered facts, row provenance and interpreted
frontier. A skipped record advances the frontier without a decision. A
recognized record produces one effective or ineffective decision. Any execution
or storage failure rolls back that record. Deterministic failures expose the
first gap; a timeout or cancelled context leaves the record available for retry
without persisting a deterministic gap.

The projection owns one writer connection and a separate read-only query
connection. Both disable extension loading and trusted schema, enable SQLite
defensive mode, forbid attached databases and bound value, SQL and expression
sizes. The query authorizer admits only application tables and views, public
host tables and a short aggregate-function allowlist. It refuses hidden row
versions, SQLite catalogs and writes. Queries are limited to one SELECT,
4096 SQL bytes, 256 rows, 256 KiB of encoded rows and a one-second deadline.
Results use the core's logical-value codec, including boolean, JSON, byte and
out-of-range integer representations, and carry the locked projection frontier.

New projection files have mode 0600. Before any writer setup, existing files
must be regular SQLite files carrying this fixture's application ID. An
unrelated file is refused without changing its bytes. Legacy JSON-affinity
columns are also refused before any writer setup, reuse or reset: their scalar
bytes may already have been coerced. There is no automatic migration.
Identity mismatch in an
owned disposable cache resets its schema; replay starts at the beginning.
Matching caches resume only when their interpreted event is still at the same
position in the supplied verified log.

Explicit continuation requires the same application and runtime plus the
core's complete writable-table compatibility check. It inspects actual stored
column types inside the transaction and refuses legacy JSON affinity even if
the old source was recompiled with the new identity. It adds newly declared
tables, replaces views and indexes, and changes the recorded identity in one
transaction while preserving rows. The query connection then adopts the new
application's allowlist. A failed transaction preserves the previous handle,
schema and rows.

## Identity and activation

The five core components come unchanged from `CoreComponents()`. A sixth
component hashes the complete dialect. The host contributes:

- `host.canonicalization=gitseq-record/2`: the full [input contract](input-contract.md).
- `host.orchestration=one-record-txn/1`: one verified record per atomic
  transaction, including application changes, decisions, facts and frontier.
- `host.projection=gitseq-query-values/1`: the core's logical query values.

The composed identity is:

```text
jsonata-ddl-runtime:sha256:d506811d6e568fc3e4c0f9773d1d0e12949cf6ab3f6bc891d8a1db7bf0aa90cd
```

The full descriptor is pinned in
[`identity.txt`](../../internal/recordruntime/testdata/identity.txt), alongside
the complete dialect in
[`dialect.txt`](../../internal/recordruntime/testdata/dialect.txt).
Tests prove each of the nine components and each limit changes identity, and
exercise core enforcement of the host's emission and confinement policy.

Only the dialect is mechanically content-hashed. Core and host component
strings require deliberate version changes when their contracts change.
Every dependency pin move remains gated by the three corpora even if those
strings do not move. I4 freezes the actual adapter inputs against the normative
contract; adapter unit tests do not replace that independent corpus gate.

I8 will place the composed digest in the host binding and projection identity
after corpus approval. Adapter reuse requires matching genesis, application,
runtime digest, source revision and storage schema. I3 records this identity
only in private fixture projections; the binding change remains a separate
delivery.

## Version skew from v0.1.1

The verified v0.1.2 source is `9280be8b9b1d610c41bc6461188fe7ecbb70bf64`.
`core.grammar=ddl/2` compiles logical JSON columns as `JSON_TEXT`, preserving
TEXT affinity for scalar numbers as well as objects. Logical exports still
say JSON. `core.value-codec=logical-values/2` preserves decoded `json.Number`
values and rejects invalid JSON. These two components change the composed
identity and the physical storage schema; the JSONata and SQLite pins stay
fixed. Real compiled-table tests exercise storage class, fold read and public
query for numeric scalar and object JSON. The legacy-affinity refusal test
checks both continuation and reopen without changing the stored bytes.

The record-admission correction independently changes
`host.canonicalization` to `gitseq-record/2`. It preserves valid envelope
bytes while rejecting malformed recognized records before normalization.
Neither version change activates or rewrites the existing host binding.

## Verification

The CI workflow runs on pushes and pull requests with read-only repository
permissions, Go 1.26.7, immutable action pins, no persisted checkout credentials
and no cache. Formatting, module verification, vet, full tests, full race tests
and build must all succeed. Python and Node run the complete twice-repeated
corpus and planted-defect gate. It has no deployment or release authority.

The [corpus gate](corpora.md) runs all three corpora twice at one clean
committed head and detects a deliberately wrong upsert in a disposable copy.
Its old oracle builds in an isolated checkout at an immutable source commit;
no successor test package imports the Gitseq spike.
