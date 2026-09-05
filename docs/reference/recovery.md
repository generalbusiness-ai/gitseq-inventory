# Fixture projection recovery

The Inventory adapter commits one verified record's application rows, row
versions, decisions, facts, provenance and interpreted frontier in one SQLite
transaction. A failed record rolls back that transaction. A separate commit
records the gap and its reason; before that commit, recovery may show the same
clean prefix with the failure not yet discovered. Completion is derived from
`interpreted_position == verified_depth` and an empty gap. The current adapter
does not store the old interpreter's separate completion flag.

`internal/recordruntime/recovery_test.go` migrates the process-death sweep from
Gitseq's retained `spike/jsonataddl/recovery_test.go` and `RECOVERY.md` at
`3f4c4969ad3afea608be521cf9b6e2422223c04e`. It runs the current application
SQL and JSONata sources through the released shared core and this repository's
actual SQLite adapter. It does not copy the removed evaluator or widen the
public fixture admission boundary.

## What the sweep checks

A test VFS wraps the OS VFS and records completed main-database and WAL writes,
truncations and deletions in order. It also records the initial rollback
journal before SQLite enters WAL mode. Volatile shared memory and temporary
statement files are excluded. An unknown durable file kind fails the sweep.
Only successful operations and the bytes actually written are recorded.

For each boundary, the test reconstructs the first *k* mutations in new files
and opens that image cold. When the next mutation is a write, it also opens a
second image with half that write applied. SQLite integrity must pass after
the initialization commit. Before initialization, an unreadable image or an
empty schema is a disposable projection that requires explicit discard and
replay; a partially initialized schema is never accepted as a valid prefix.
The fixture opener refuses an incomplete existing file rather than inventing
identity or continuing from an absent frontier. The operator must preserve
any needed evidence and choose a fresh projection path for verified replay.

Every readable image must report the expected committed record position,
matching event identifier, verified depth and gap metadata. Its complete
logical table dump must equal a clean replay at that prefix. The comparison
includes hidden row versions, application identity, decisions, facts and row
provenance. The frontier is checked separately. The test covers multi-frame
event commits, a mid-replay truncate checkpoint and the close-time checkpoint.

The clean run uses the existing signed 14-record corpus. The rollback run
injects a failure after the first stock record's tentative row changes, before
its frontier commit, then uses the real adapter to record the gap. This tests
rollback after writes, not only a rejected input before storage. A reference
run checked 504 images over 254 mutations for clean replay and 138 images over
70 mutations for rollback/gap recovery. Counts are recorded observations, not
portable constants; the test requires the interruption categories themselves.

An executed mutation commits the application transaction before updating the
frontier in another transaction. The sweep rejects a cold image whose decision
and fact rows describe record 3 while the frontier remains at record 2. This
proves the prefix comparison is sensitive to the atomicity rule it claims.

## Limits

The model is process death: completed writes remain present in order. The
half-write variant adds one bounded torn-write case. The sweep does not model
lost or reordered unsynced writes, arbitrary byte corruption, filesystem loss,
OS crashes or power failure. It makes no stronger durability claim from
SQLite's synchronization setting.

The public runtime still admits only the sealed four-record demonstration.
The broader signed corpus is exercised inside private tests. Deterministic
production execution/allocation bounds are still required before broader
replay can be exposed. Runtime identity, dependency pins, legacy JSON-affinity
refusal, query authority and host-owned transactions are unchanged.

The retained Gitseq recovery note also described temporary shadow views and
historical query experiments. Those observations remain historical evidence
at the source commit above. This migration does not restore historical
execution, temporary shadow schemas or the old evaluator's authority rules.
