# Inventory corpus gate

Run `python3 scripts/verify-corpora.py` at a clean committed candidate with
Git, Go 1.26.7, Node and Python 3.12 or later. It needs the public Go proxy,
checksum database and GitHub source repository. It prints an external temporary
evidence directory containing commands' output and a final `summary.json`.
It does not change the candidate or update goldens. Ordinary `go test ./...`
also runs B, C against sealed oracle rows, and the input freeze; the delivery
gate additionally executes the live old oracle on both repetitions.

## A: immutable shared-core conformance

The gate verifies `github.com/generalbusiness-ai/tailapps/jsonataddl v0.1.2`
and its exact module sums from [architecture](architecture.md), the unchanged
JSONata/SQLite pins, no replacements and no Tailapps root module dependency.
It clears Go bypass variables, disables workspaces, fixes Go 1.26.7 and uses
the public proxy and checksum database. It never supplies `-update-corpus`.

It runs the upstream module's own tests:

```sh
go test -mod=readonly -count=1 -json \
  -run '^TestConformanceCorpus(ProjectionCases)?$' \
  github.com/generalbusiness-ai/tailapps/jsonataddl
```

Both suites and every case named by the immutable manifests must explicitly
pass. Missing tests, skipped tests and a changed corpus are failures. The
upstream runner and goldens stay in the released module, outside this repository.

## B: live JSONata reference

`TestCorpusB` retains the 18 existing cases and `reference.js` from Gitseq
`79e8400888a00a38ccdf96722eebcba2491a9780`. The reference executes jsonata-js
2.0.6 supplied by the pinned JSONata Go module. Every deterministic case must
match its frozen value and the live reference, repeatedly in Go. The core must
refuse every ambient and order-dependent expression, including wildcard forms
the old text guard missed, plus `$millis` and `$eval` named in the old report.
Case classifications describe the old experiment; they do not grant admission
in the new core. A deterministic expression may still be outside its language.

These two files and the original inventory fold carry the upstream MIT terms
in `internal/recordruntime/testdata/LICENSE.gitseq`. The Apache-2.0 shared core
is a dependency, not copied source. This is a focused compatibility gate, not
proof of deterministic step or allocation bounds.

## C: one sealed log, two runtimes

`testdata/inventory.bundle` contains only the signed sequence ref, with no
private keys. Tests verify the bundle hash and replay its records through the
public host before interpretation:

```text
SHA-256: 01fd236ddab6f47db1c5a97bcbf716ad6ce02fee03de94baf05d5cce8084149d
genesis: 1e646a0c244cb07748e54f252ad92800fabd1996
head: 94eb93c7313b90d7808cb5db901aed3066a4c0c8
depth: 14
```

Its old spike binding is verification input, not a new application activation.
The read-only locator configuration supplied in a temporary repository carries
no signing key. Both runtimes receive the same verified records. The old
`spike/cmd/jsonata-inventory` executable is built unchanged at Gitseq
`79e8400888a00a38ccdf96722eebcba2491a9780` in an isolated checkout with its own
module graph. Current application tests import no oracle package, and deleting
the spike from future Gitseq heads does not remove the pinned historical source.

The fixture covers both current inventory event schemas: missing stock,
repeated receipts, effective and refused reservations, exact depletion,
multiple SKUs and an exactly representable maximum integer. Unrelated malformed
payloads, case-changed schemas and the binding record produce no decision.
The old embedded fixture's extra cancellation vocabulary is not exercised;
it is not part of this inventory application's declared input set.

The gate compares complete ordered decisions `(position,event_id,event_type,
decision)`, ordered facts `(event_id,ordinal,kind,fact_json)`, and final stock
and reservation rows. The frozen oracle has 11 decisions, three refusal facts,
three stock rows and four reservations. The only exclusions are **runtime/source
identity** and **program names**. There is no exclusion for record IDs, absent
decisions, fact bytes or application state. The current identity is checked
separately by its nine-component freeze and component-change tests.

Corpus C reads the actual root `application.sql`, `folds/normalize.jsonata`
and `folds/inventory.jsonata`, the same files embedded by `Load()`. There is no
duplicate fixture application. Each repetition also runs the public application
binding, replay, query and closed-fixture admission tests. The gate checks
`go list -deps -test ./...` and refuses any active or retained test spike import. The two runs must match both sealed oracle rows and
the live historical executable. Finally a disposable archive of the exact head
subtracts one from each receipt's upsert. C's stock comparison must fail; a
build error or unrelated failing test does not satisfy this planted-defect gate.

## Complete input freeze

Four independently encoded goldens cover all eight envelope members plus
`meta:{}` and `rows:{}`: empty causals, two causals in non-sorted kernel order,
the exact integer bound `9007199254740991`, and a synthetic first-position
case with a negative Unix-seconds timestamp and duplicate causal IDs. The
first three records come from the sealed log; the fourth varies the known
record only to test input conversion at those boundaries. Full byte comparison
detects reordering, deduplication, null empty objects or changed numeric decoding.
No golden comes from the old interpreter's differently shaped evaluation input.

The scalar JSON storage/read/query and legacy-affinity continuation/reopen
tests run beside both corpus repetitions. No test or timeout authorizes
production replay: the public command admits only the closed demonstration;
broader runtime entry points remain private to these tests.
