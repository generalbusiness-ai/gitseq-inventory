# Declare the complete JSONata input contract

Decision commissioned by Tailapp request `e919a5d70d7cd1429ff83d57e65f05fc570fb2aa`,
I5 of the adopted Gitseq shared-core plan at `860ee61a`. Basis: Tailapp
`9280be8b` / released `jsonataddl v0.1.2`, and Inventory's input freeze at
`12c1687b` (`docs/reference/input-contract.md`). Correction request `b2e5cf67`
addresses ratified review `b599714c`.
Implementation follows the decision's independent approval, sealed delivery
and satisfaction of both design obligations; this head releases nothing.

## Decision

Add a small, immutable input contract to `Dialect`. Describe `meta` for both
stages and the normalizer's complete host event. Keep the existing scalar
envelope as the only host fields eligible for SQL read parameters. Structured
fields are program inputs, never new SQL parameters. Analytic event shape
continues to come from the declared private event; read-result shape is derived
from the existing read plan and enforced as
described below. No second user declaration is needed for those contracts.

Use a closed set of forms, not a schema language:

| Form | Meaning |
|---|---|
| Scalar member | Existing TEXT, INTEGER, REAL, BOOLEAN or BLOB logical value; explicit required/optional and nullable/non-null flags. |
| String array | JSON array of strings, preserving order and duplicates. Empty is `[]`; null is distinct. |
| Closed scalar object | Exactly the declared scalar members, each with its own presence/type/nullability. No recursive object declarations. |
| JSON object | Object with arbitrary JSON-valued members; nested keys are deliberately opaque to this core contract. |

`meta` is a closed object of scalar members, including the empty object. The
host event is a closed object combining envelope scalars and separately named
structured members from the forms above. No references, unions, predicates,
regexes, callbacks, capability registry or new DDL syntax are introduced.
Application-specific meaning inside opaque JSON stays in the existing host
canonicalization component and its byte-level conformance fixtures.

Every contract states whether its root object permits null. A missing map
(`nil`, encoded as null) is not `{}`; no default values are synthesized.
Use `Optional bool`: false requires the key, preserving a strict zero value.
`Nullable bool` independently permits null; false refuses it. An optional
non-null member may be absent but may not be present as null.
Unknown members refuse. An entirely unspecified input contract refuses at
compile/load time; it never selects the old unchecked behavior silently.
Constructors and accessors defensively copy nested member lists.

Validate `EnvelopeField.Type` at dialect admission: accept exactly the five
canonical uppercase scalar types above, and reject JSON and unknown spellings.
JSON is supported by the shared *column* value model, but is not a scalar
host read parameter. Use the structured declaration for a JSON input. Reject
duplicate/invalid names and collisions between scalar and structured members.
Adding `Optional` preserves named struct literals and keeps existing envelope
fields required. Callers must still supply the complete input contract.

## The two real hosts

| Input | Tailapp producer at `9280be8b` | Inventory freeze at `12c1687b` |
|---|---|---|
| `meta`, both stages | Required INTEGER `position`, TEXT `event_id`, TEXT `event_type`; non-null root and members. | Required non-null empty object; no members. |
| Normalizer scalars | The nine existing envelope fields, all present; existing nullability remains, including nullable time/trace/span strings. | The six existing TEXT fields, all present and non-null; position and timestamp remain strings. |
| Structured event members | Required non-null `record`, an opaque JSON object decoded with number preservation from canonical OTLP record bytes. | Required non-null `rests_on` string array; required non-null closed `payload` object with TEXT `id`/`sku` and INTEGER `qty`, all required and non-null. |
| Analytic event | Existing private `otel_event` declaration. | Existing private `inventory_event` declaration. |

Inventory retains its host-side recognized-schema check, signed canonical
payload-byte check, positive quantity rule, 8 KiB payload bound and exact empty
metadata. The input contract does not move admission after evaluation or grant
production replay. Array sorting, deduplication, timestamp conversion, absent
metadata and extra payload fields remain forbidden by that freeze.

## Fixtures are an explicit migration

Keep the production Tailapp constructor strict. The implementation must make
these reviewed test-data changes, rather than silently substituting a dialect:

- Remove only vestigial `meta.emission_ordinal` from the eight released corpus
  inputs (two `basic/inputs/accumulate-*`, six `misbehavior/inputs/*`). Preserve
  their other bytes and intended result/error goldens. The actual producer
  encodes the ordinal in `meta.event_id`; it never supplies that extra key.
- Replace abbreviated production-Tailapp Go inputs, including
  `internal/profile/runtime_test.go` and `tailapps/embed_test.go`, with complete
  fixture inputs built from their known source record, position and emitted
  event ID/type. Assert the producer-shaped input explicitly; do not coerce an
  incoming partial value or add an evaluator default. Keep the existing
  application outcomes. Inputs whose purpose is omission become refusal tests.
- Make public `ExampleLoadApplication` demonstrate a complete Tailapp input:
  all three metadata fields, all nine envelope fields and a canonical record
  object, with nullable fields explicitly null and `rows: {}`. Its printed
  result remains `effective`; abbreviated input becomes a separate negative
  example. Renamed-host tests declare their own complete scalar/`body` contract.

The corpus runner continues to use the production Tailapp dialect. Its input
freeze changes deliberately at this version boundary; retain the old bytes in
the immutable v0.1.2 release and explain the migration in `corpus/README.md`.
Do not claim new inputs are byte-identical to the historical extraction corpus.
Run and review regenerated identity/diagnostic goldens individually; an input
rejection must not hide the output defect a misbehavior case is meant to test.
Actual valid Tailapp/Inventory metadata and event bytes and semantic array
order remain unchanged. The explicit input-depth and empty-read changes below
are separately versioned; do not claim universal byte compatibility. Inventory's host payload checks retain the signed-byte, recognized
schema, positive-quantity and 8 KiB restrictions; common shape checks may remain
as defense in depth, with the core declaration tested against the host freeze.

## Validation and identity

`Evaluate` resolves the program, serializes the input once, applies the existing
input-byte bound, and validates that same encoded representation before
invoking JSONata. Decode numbers with `UseNumber`; validate INTEGER/REAL using
the existing logical-value rules, BLOB as base64 text, and nested JSON without
coercion. Native Go integers therefore remain the JSON numbers they already
encode as; they do not become strings. Apply exact-range INTEGER and finite
REAL rules only to declared scalar
members. Opaque JSON numbers, including legal JSON such as `1e999`, retain
their existing representation and evaluator behavior. Reject invalid JSON.
Add a separate positive `Limits.MaxInputDepth`, counted over the encoded
evaluation input (root object depth 1), with 1024 in both host constructors.
This deliberately refuses formerly accepted inputs deeper than 1024 under the
new identity. It does not reuse `MaxDepth`, which remains JSONata execution
depth; inputs nested beyond 64 must have a positive validation regression.
Byte and depth checks precede recursive contract walks; do not coerce numbers.
Pass the original encoded bytes to the evaluator after validation. Keep the
existing evaluator confinement and the unresolved production work/allocation
gate; this change does not claim to supply that gate.

Validate metadata for every program and the complete host event for a
normalizer. Analytic events follow their declared private-event columns.
Expose `Application.ValidateProgramInput(programName string, meta, event
map[string]any) error` for the pre-read check. It resolves the program and
validates the encoded metadata/event under the same byte/depth limits, without
rows. Hosts call it before binding values to a read query. `Evaluate` applies
the same validator to its complete encoded input, then validates `rows` before
calling JSONata. The pre-read check is not permission to bypass full validation.

`rows` is a required non-null closed object: exactly one key per declared read,
or `{}` when there are no reads. Derive its contract from the compiled plan:
`ONE` is one non-null object, `OPTIONAL ONE` is an object or null, and `MANY` is
a non-null ordered array with zero through `Limit` objects. Every row has
exactly the selected column names. For a table read, enforce each selected
column's declared logical type and nullability, including JSON-valued columns;
all keys are present even for nullable columns. For a view read, preserve its
selected-column shape and validate values as bounded JSON, including null:
views currently declare SQL, not logical result types. Do not invent typed
view metadata or claim `ReadRowValue` validates these values. View SQL and the
read plan already enter application revision identity; no new DDL is required.
Unknown/missing read or column names, wrong cardinality and malformed values
refuse direct callers as well as host-produced rows. Keep `ReadRowValue`'s
existing Boolean/JSON/BLOB conversions; validate their encoded result.
Tailapp currently marshals an empty `MANY` result from a nil slice as null.
Change its read producer to construct an empty slice, yielding `[]`, and test
this explicit input-semantic change with the orchestration version bump.
Direct `MANY: null` inputs then refuse; do not silently coerce them in the core.

Failure rolls back application changes and the interpreted frontier. Existing
host gap reporting may persist failure metadata separately after rollback
(`projection.go`'s gap path); this is not a committed interpreted record.

Version `Dialect.Canonical` to an unambiguous encoding of the whole dialect,
including these contracts. Use fixed-order JSON records with explicit fields
(no omitted defaults); sort object-member declarations by name, escape strings
with Go's `encoding/json` string encoding, and preserve semantic array order.
Include kind, optionality, nullability, member types and root-null policy;
include `MaxInputDepth` alongside all existing limits. Reject duplicates before
encoding. Reordered object declarations have one identity; every semantic
contract change has a different identity. Type admission and string escaping
must defeat the current canonical collision: two fields `id TEXT` and
`signal TEXT` equal one `id` field whose type is
`TEXT/nullable=false\nhost-event.field.signal=TEXT` (both non-null).
The escaped `\n` here denotes a literal newline in the crafted type. Preserve producer canonicalization and byte
goldens; an opaque object's inner semantics still belongs to its host.

Keep the closed component set: `core.interface`, `core.grammar`, `core.jsonata`,
`core.value-codec`, `core.sqlite`, `dialect`, `host.canonicalization`,
`host.orchestration`, `host.projection`. Core changes `core.interface` and the
built-in `tailapp-otlp` dialect version/digest. Do not add a redundant input
semantic-version field. Tailapp also bumps `host.orchestration` for empty-read
production and the new continuation refusal below; its canonicalization and
query values stay fixed.
Inventory owns its separate `GitseqRecord()`/`gitseq-record` version/digest bump
at consumer adoption; it already produces empty `MANY` as `[]`, so its
transaction and admission rules stay fixed. Pins,
grammar, evaluator, value codec and SQLite components remain unchanged.

Update Tailapp's actual composed descriptor/digest in
`internal/profile/runtime_test.go` and Inventory's native identity fixture.
The module corpus uses the separate literal `corpusRuntimeProfile`; it is not
a composed-digest fixture. Assign a new explicit corpus runtime literal for the
new input semantics and review its regenerated revision goldens. Preserve the
legacy host runtime literal used to recognize old stored projections.

## Old storage requires an owned continuation change

Today Tailapp `OpenForUpgrade` bypasses runtime equality, while
`ContinueCompatible` checks only table shapes and `Projection.Continue` can
rewrite the stored runtime with old rows intact. The implementation must close
that path before releasing this input contract. This is a required host change,
not a claim about behavior already present.

Builder owns three linked guards. `ContinueCompatible` refuses unequal profile
runtimes as well as incompatible tables. The engine checks the actual stored
runtime before its activation journal or queue detachment. `Projection.Continue`
checks that same persisted runtime inside its transaction before any schema,
frontier or identity write. A profile recompiled under the new core cannot
stand in for the stored runtime: `OpenForUpgrade` must retain the physical
identity for the engine check. Refuse every runtime mismatch, including this
input-contract transition; same-runtime compatible-source continuation remains.

Old projections may stay available for the existing upgrade-pending query
path, with delivery disabled. Crossing runtimes requires the existing explicit
acknowledged reset into a fresh projection; preserve its documented activation
boundary rather than promising a new historical replay facility. Old compiled
handles are never mutated or relabeled. Test direct continuation, engine
activation and recovery from a pending activation journal: old rows, frontier,
stored identity and queue must remain unchanged on refusal. Removing the
persisted-runtime guard must reproduce unsafe relabeling even when table shapes
match and the recovery profile was compiled with the new runtime.

Inventory continues to require a fresh projection and an explicitly authorized
new signed binding for activation. No binding or signed record is rewritten.

## Delivery gates and owners

1. **Builder / checker — core and Tailapp integration.** Implement the contract,
   admission, encoded input/row checks and continuation guards together. Pass
   scalar/collision/identity omission mutants, direct/pre-read and all read
   cardinality cases, immutable-copy and separate-depth-limit tests, producer
   byte goldens, migrated corpus/Go/public-example cases, and unsafe-continuation
   red proofs. Old-runtime reset needs acknowledgement; same-runtime compatible
   continuation stays green. Run complete core/host and race suites. Update
   `docs/reference/ddl-jsonata.md` (input and read shapes), `otel-records.md`
   (complete envelope and limits), `docs/reference/cli.md` activation/upgrade rules and
   corpus migration instructions in the same reviewed head.
2. **Builder / checker — release.** After sealed integration, run module boundary
   and public-resolution/release gates and publish a separately reviewed nested
   module version. Pin the exact version/checksums and composed identity; this
   note publishes none.
3. **Inventory implementer / native reviewer — consumer adoption.** Adopt that
   release, bump its owned `gitseq-record` dialect version, declare the exact
   freeze and enforce pre-read/full-input checks. Update its runtime fixture
   and `docs/reference/input-contract.md`; pass both full corpus repetitions,
   input byte goldens, row/shape/admission mutants and old-binding/storage
   refusal. Deliver natively. Broader activation remains separately gated.

The affected layers are the core compiler/evaluator, Tailapp profile,
projection and engine activation, their corpus/reference documents, and the
consumer's private record adapter. No production activation, generic schema
language, capability registry, historical evaluator or I7 work enters I5.
