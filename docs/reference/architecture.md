# Inventory architecture

The inventory application runs outside the Gitseq sequencing kernel. Gitseq's
public `host` package verifies signed records and application bindings. SQL,
JSONata and disposable SQLite projections belong to this application.

## Current delivery boundary

The public application loader and commands still use the pinned Gitseq spike
runtime. Their binding remains `jsonata-v206-sqlite-spike@0`.
`internal/recordruntime` defines the successor dialect and identity only; it
does not yet interpret records, open databases or replace that binding.

This is I2 of the adopted design
`notes/2026-09-04-tailapps-jsonataddl-adoption.md` at Gitseq commit
`860ee61a07aa753dcbc2d50e74da2b7b6547625b`, sections 2.3 and 5.
I3 supplies host adapters, I4 supplies differential and input-contract corpora,
and I8 switches the application only after those deliveries pass review.

## Layers and contracts

| Layer | Owner and boundary |
|---|---|
| Verified records and binding (Gitseq layer 4) | `github.com/generalbusiness-ai/gitseq/host`; signed record bytes, ordering and binding selection remain unchanged. |
| Application interpretation (Gitseq layer 5) | Inventory's internal `recordruntime` supplies policy to the external `jsonataddl` core. I2 adds a dormant successor contract; it does not change the active interpreter. |
| Application surface (Gitseq layer 6) | Existing fixture and query commands; no new command, service or production activation in I2. |

The shared core is exactly
`github.com/generalbusiness-ai/tailapps/jsonataddl v0.1.1`:

```text
Sum: h1:rzas2PYo0x3n9K6lLo42l3ZLyN16VwHpUjDMtRFZaRg=
GoModSum: h1:fpGrE/1ODSULhyxThhr3TL4JtpfcX3PdA4kJBnKWJRY=
```

Its two direct dependencies remain `github.com/jsonata-go/jsonata`
`v0.0.0-20250709164031-599f35f32e5f` and `github.com/ncruces/go-sqlite3`
`v0.35.3`. The core is Apache-2.0 licensed with LICENSE and NOTICE shipped in
the module. No core implementation or corpus is copied here, and there is no
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
policy and supplies the default-deny authorizer that I3 must install.

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
production interpretation is safe. I2 exposes no successor replay entry point.

## Identity and activation

The five core components come unchanged from `CoreComponents()`. A sixth
component hashes the complete dialect. The host contributes:

- `host.canonicalization=gitseq-record/1`: the full [input contract](input-contract.md).
- `host.orchestration=one-record-txn/1`: one verified record per atomic
  transaction, including application changes, decisions, facts and frontier.
- `host.projection=gitseq-query-values/1`: the core's logical query values.

The composed identity is:

```text
jsonata-ddl-runtime:sha256:f07be2dbe33393c87919de6868420ee27218245a2d410fc2a81f498b31e620cb
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
contract; I2's identity tests do not claim that adapter evidence exists yet.

I8 will place the composed digest in the host binding and projection identity
after corpus approval. Reuse will require matching genesis, application,
runtime digest, source revision and storage schema. Those adapters and the
binding change are separate deliveries, not implemented by this page.

## Verification

The CI workflow runs on pushes and pull requests with read-only repository
permissions, Go 1.26.7, immutable action pins, no persisted checkout credentials
and no cache. Formatting, module verification, vet, full tests, full race tests
and build must all succeed. It has no deployment or release authority.
