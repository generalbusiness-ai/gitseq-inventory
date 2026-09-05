# Verified record input contract

This is the normative meaning of `host.canonicalization=gitseq-record/2`.
It preserves the accepted input bytes specified by the adopted design at
Gitseq commit `860ee61a07aa753dcbc2d50e74da2b7b6547625b`, section 2.3, and
corrects its record-admission boundary under inventory request
`3761e1f40afc1fbe71c2007d8eccb1ea781a376b`. I3 implements it;
I4 freezes the produced bytes. I5 declares their complete shape through public
`jsonataddl v0.2.0`, with Inventory’s owned `gitseq-record/2` dialect.
I8 uses the same bytes behind the closed demonstration admission boundary;
broader replay remains confined to in-package tests.

Admission precedes envelope construction. Only the exact schema strings
`stock_received` and `reservation_requested` belong to this application.
Other schemas advance the frontier within their record transaction without
decoding the payload or creating decisions or facts, even when their payload
is malformed, scalar or oversized. This preserves the old interpreter's skip
behavior; no normalizer runs for those records.

Recognized payloads must fit the 8 KiB ceiling, contain one JSON object, and
equal the number-preserving `json.Marshal` encoding of that object byte for
byte. Duplicate keys, extra whitespace, changed key ordering and trailing
data therefore fail. The object has exactly `id`, `sku` and `qty`: `id` and
`sku` are non-null TEXT, and `qty` is a positive, exactly representable JSON
integer, matching both existing inventory event declarations. The core owns
logical type validation. This closed application policy adds no input keys,
metadata, DDL syntax or general extension mechanism.

An admitted record must produce exactly one private event. A normalizer that
emits none causes an interpretation failure; it cannot silently discard a
recognized record. Any admission or execution failure leaves the interpreted
frontier before the record and adds no decision, fact or application change.
The former `/1` host identity has never been activated. This correction changes
identity to `/2`; it does not rewrite an existing binding.

For both the normalizer and analytic fold, `meta` is the non-null empty object
`{}`. The normalizer declares no reads, so its `rows` is also `{}`. An
analytic fold receives the named results of its declared reads in `rows`.
Absent metadata, `null` metadata and additional metadata keys are forbidden.

The normalizer's `event` contains exactly these eight members:

| Member | Source and exact encoding |
|---|---|
| `id` | `host.Record.ID` verbatim as a JSON string. Canonical Gitseq event ID, with no normalization. |
| `schema` | `host.Record.Schema` verbatim as a JSON string. No case folding. |
| `actor` | `host.Record.Actor` verbatim as a JSON string: the verified 64-character lowercase hexadecimal public-key fingerprint. |
| `position` | The record's one-based index in `host.Log.Records`, formatted as an unpadded unsigned decimal JSON string. The first record is `"1"`. |
| `timestamp` | `host.Record.Timestamp`, signed Unix **seconds**, as an unpadded base-10 JSON string. A minus sign appears only for negative values; no fraction or suffix. |
| `payload_digest` | `"sha256:"` plus 64 lowercase hexadecimal characters from SHA-256 over `host.Record.Payload` bytes exactly as signed. No decoding or re-encoding before hashing. |
| `rests_on` | `host.Record.RestsOn` as a JSON array of strings in verified kernel order. Preserve duplicates; never sort or normalize. An empty chain is `[]`, never `null`. |
| `payload` | The signed payload decoded with the core's `DecodeCanonical`, preserving numbers as `json.Number`. It must be an object. Non-object, invalid or oversized input fails before evaluation. |

The six TEXT scalars are declared envelope fields. `rests_on` and `payload`
are deliberately not envelope fields and cannot become scalar read parameters.
The host enforces an 8 KiB payload ceiling before decoding as well as the core's
32 KiB complete evaluation-input bound. Neither bound changes the signed bytes.

The analytic fold's `event` is the private `inventory_event` emitted and
validated by the core, not the original record envelope. Any required new
application data belongs in declared private-event columns, not ambient
metadata. The fold input still uses the core's logical-value codec.

Changing any source, conversion, encoding or metadata rule changes this host
component and requires a new binding. The dialect mechanically binds the required non-null empty metadata, six
required TEXT fields, required non-null `rests_on` string array and required
non-null closed `payload` object with TEXT `id`/`sku` and INTEGER `qty`.
Optionality and nullability default to refusal; unknown members refuse.
The host keeps its signed-byte, recognized-schema and positive-quantity rules.

The adapter calls `ValidateProgramInput` before executing reads. The core
validates the same complete encoded input before evaluation, including exact
read names, cardinalities, columns and logical values. Empty MANY remains `[]`.
`MaxInputDepth=1024` counts the evaluation root as depth one and is independent
of the existing evaluator depth 16. The 32 KiB complete-input bound remains.
The four I4 byte goldens stay unchanged: complete input JSON, each scalar, empty
and ordered/duplicate causal chains, and numeric payload at the exact integer
bound are checked against this declaration, without normalizing historical data.

## Existing bindings and projections

The previous input runtime cannot activate this interpreter through an old
signed binding. Existing projections with a different persisted runtime refuse
before writer setup or automatic reset, and continuation checks persisted
runtime inside its transaction even when compiled handles are current. This
changes `host.orchestration` to `one-record-txn/2`; the canonical input bytes,
one-record transaction and query-value components remain unchanged. Same-runtime
cache resets and compatible continuation retain their existing behavior.

Refusal preserves durable database, WAL and journal data, stored identity,
rows and frontier. The normal read-only SQLite probe reads committed WAL state
and may leave volatile SHM or a newly created zero-byte WAL. It does not create
transaction frames, checkpoint, delete sidecars or open an application writer.
A fresh projection and an explicitly authorized binding are required; this
source adoption supplies neither production activation nor an automatic
migration. The public surface remains the closed demonstration.
