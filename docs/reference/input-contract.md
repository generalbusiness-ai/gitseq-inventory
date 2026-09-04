# Verified record input contract

This is the normative meaning of `host.canonicalization=gitseq-record/1` from
the adopted design at Gitseq commit
`860ee61a07aa753dcbc2d50e74da2b7b6547625b`, section 2.3. I3 implements it;
I4 freezes the produced bytes. I2 defines the contract only.

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
The host must enforce a payload ceiling before decoding as well as the core's
complete evaluation-input bound. Neither bound changes the signed bytes.

The analytic fold's `event` is the private `inventory_event` emitted and
validated by the core, not the original record envelope. Any required new
application data belongs in declared private-event columns, not ambient
metadata. The fold input still uses the core's logical-value codec.

Changing any source, conversion, encoding or metadata rule changes this host
component and requires a new binding. The upstream dialect does not bind the
non-scalar inputs or metadata mechanically. Therefore the I4 freeze must check
the complete input JSON and each scalar, empty and ordered causal chains, and
numeric payload preservation at the exactly representable integer bound.
