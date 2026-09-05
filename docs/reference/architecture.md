# Inventory architecture

The inventory application runs outside the Gitseq sequencing kernel. Gitseq's
public `host` package verifies signed records and application bindings. SQL,
JSONata and disposable SQLite projections belong to this application.

## Current delivery boundary

The application and commands use shared `jsonataddl v0.2.0` with this
repository's host adapters. `Binding()` records the composed runtime digest;
`Load()` compiles the three embedded SQL/JSONata files. `OpenFixture()` verifies
the binding and signatures through Gitseq's public host before any application
interpretation. The old spike interpreter is absent from active and test imports.

Only the documented demonstration can activate a projection: one initial
binding followed by the exact canonical receipt of five ink units and the two
reservation requests for two and four units. The three actors must equal the
binding actor, and their causal lists must be empty. Signed IDs, keys and
timestamps may vary between generated examples. Count, schema or payload changes
are refused before evaluation, database opening or cache mutation. There is no
flag to admit a production log. The returned handle exposes only bounded queries
and close; replay and continuation remain private.

This completes the application migration described in sections 2 and 5 of
`notes/2026-09-04-tailapps-jsonataddl-adoption.md` at Gitseq commit
`860ee61a07aa753dcbc2d50e74da2b7b6547625b`, under the I8 assignment. The source
uses a read-free normalizer, one private event and one analytic fold. Both base
tables have exported reads. The fold branches on private `event.kind` and
explicitly projects reservation columns. The host's recognized-schema and
payload validation remains in front of normalization.

## Layers and contracts

| Layer | Owner and boundary |
|---|---|
| Verified records and binding (Gitseq layer 4) | `github.com/generalbusiness-ai/gitseq/host`; signed record bytes, ordering and binding selection remain unchanged. |
| Application interpretation (Gitseq layer 5) | Inventory's internal `recordruntime` owns verified-record conversion, read execution, mutation transactions and disposable projections. The external `jsonataddl` core owns compilation, evaluation, validated results, read plans and logical values. The fixture binding selects the composed shared-core runtime. |
| Application surface (Gitseq layer 6) | Fixture generation and bounded query commands; the public entry point mechanically refuses logs outside the closed demonstration. |

The shared core is exactly
`github.com/generalbusiness-ai/tailapps/jsonataddl v0.2.0`:

```text
Sum: h1:rD0TyYRPHT+DapFEacbd+jLQKHo6I+mi2ufpcCd+eKY=
GoModSum: h1:fpGrE/1ODSULhyxThhr3TL4JtpfcX3PdA4kJBnKWJRY=
```

Its two direct dependencies remain `github.com/jsonata-go/jsonata`
`v0.0.0-20250709164031-599f35f32e5f` and `github.com/ncruces/go-sqlite3`
`v0.35.3`. The core is Apache-2.0 licensed with LICENSE and NOTICE shipped in
the module. No upstream core implementation or conformance corpus is copied here, and there is no
dependency on the Tailapps root module. Gitseq remains pinned for its public
verification host; no compiled or retained test package imports its spike.

Adding the core selects `golang.org/x/text v0.41.0` instead of `v0.40.0` in
the module graph. It is used by SQLite's dependency tests, not inventory's
compiled runtime packages. The two required runtime pins above do not move.

## Dialect and limits

`GitseqRecord()` returns a fresh `gitseq-record/2` dialect. Sources are
`application.sql` and `folds/*.jsonata`. The host event is `gitseq_record`;
the six non-null TEXT envelope fields are `id`, `schema`, `actor`, `position`,
`timestamp` and `payload_digest`. Causal arrays and decoded payload objects
are required structured normalizer inputs, not scalar read parameters. The
complete input contract requires non-null empty metadata, the ordered string
array `rests_on` and a closed `payload` object with TEXT `id`/`sku` and
INTEGER `qty`. The existing signed-byte admission remains ahead of evaluation.

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
| Input nesting depth | 1024 | Independent encoded-input bound, root depth one. |
| Evaluation output | 16 KiB | Small event or mutation result. |
| Evaluator depth | 16 | Shallow record and row expressions. |
| Range | 64 | Conservative fixed dialect bound; it grants no range syntax. |
| Emitted events | 1 | One verified record must not yield multiple decisions. |
| Facts | 8 | Allows a short diagnostic list. |
| Row changes | 8 | Inventory requires at most two; leaves a small fixed margin. |
| MANY read rows | 64 | Finite future read ceiling; inventory currently needs one row. |

The eleven existing bounds remain unchanged; input nesting adds the separate
1024-depth limit used by the upstream constructor.
These are source/value bounds, **not deterministic evaluator step or allocation
bounds**. The runtime remains fixture-only until both missing bounds exist. A wall-clock timeout is a retryable machine failure, never evidence that
production interpretation is safe. The public command admits only the fixed
demonstration above; the broader signed corpus is interpreted only by tests.

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

The read adapter validates complete metadata and private-event input through
`ValidateProgramInput` before it binds declared event parameters, enforces ONE, OPTIONAL ONE and bounded MANY
cardinalities, and uses the core's logical read values. It seats the core's
read authorizer only for that read plan and clears it on every return path.
Before JSONata runs, the core checks full encoded input and the declared read
result names, cardinalities, columns and logical values. Empty MANY remains
`[]`. Host writes occur after the seat is cleared; evaluated programs cannot submit
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
jsonata-ddl-runtime:sha256:49b8c6adb62aa8cb141bdf126b890d19e519ca72693d369a46b3b95193b4291d
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

The host binding now selects this composed digest. Projection identity records
the same digest, source revision, storage schema and export contract. Tests read
the stored identity and compare it with the binding and compiled handle. Reuse
requires matching genesis, application, runtime digest, source revision and
storage schema. The old binding is refused; no command replaces an existing
log binding or migrates production storage.

## Earlier value-codec and admission migrations

The earlier verified v0.1.2 source is `9280be8b9b1d610c41bc6461188fe7ecbb70bf64`.
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
I8 activates the resulting identity for newly generated demonstration logs;
existing bindings and storage are never migrated automatically.

## Declared-input adoption

The verified public v0.2.0 source is `8c674fa9ecb4797f7de4322ac97cb3ffe2d21672`.
It changes `core.interface` to `jsonata-ddl-application-interface/2026-09-05`
and canonically encodes the whole input declaration. Inventory owns its
`gitseq-record/2` dialect and digest; it does not copy Tailapp’s dialect or
host components. Its eight-member input bytes and closed fixture admission
remain unchanged. The corpus gate explicitly checks the migrated upstream
corpus and the unchanged native input/record goldens.

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

## Process-death recovery evidence

The [recovery sweep](recovery.md) exercises the current layer-5 adapter and
shared core using a private recording VFS and an observation after each
committed record. Layer 4 still supplies verified ordered records; layer 5
still commits rows, decisions, facts, provenance and the interpreted frontier
in one host-owned SQLite transaction. Completion remains derived from the
stored frontier, and gap metadata commits separately after rollback. Layers
6 and 7 retain bounded completed-fixture reads and closed demonstration
admission. No public API, runtime identity, dependency pin, authority boundary
or production activation changes. Incomplete initialization requires explicit
discard and verified replay; the sweep makes no power-failure guarantee.
