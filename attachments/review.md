# README current-runtime correction

The README now names the released v0.2.0 core, links to the authoritative identity fixture instead of copying a digest, and distinguishes same-runtime source rebuild from stored-runtime refusal. Its shell examples, fixture-only admission, no production override and missing evaluator step/allocation bounds are unchanged.

Reviewed current sources (all preview at this immutable candidate head):
- [docs/reference/architecture.md](docs/reference/architecture.md@0a2510b1911f4bf2490a5f370f49536e689ab663:186)
- [go.mod](go.mod@0a2510b1911f4bf2490a5f370f49536e689ab663:7)
- [internal/recordruntime/projection.go](internal/recordruntime/projection.go@0a2510b1911f4bf2490a5f370f49536e689ab663:194)
- [internal/recordruntime/testdata/dialect.txt](internal/recordruntime/testdata/dialect.txt@0a2510b1911f4bf2490a5f370f49536e689ab663:1)
- [internal/recordruntime/testdata/identity.txt](internal/recordruntime/testdata/identity.txt@0a2510b1911f4bf2490a5f370f49536e689ab663:1)

[README.md](README.md@0a2510b1911f4bf2490a5f370f49536e689ab663:1) is the only changed file. Earlier exact source revisions and byte comparisons are retained separately in source-provenance.json.

The prior README publication327 rests on I8 receipt9839c792. Its candidate320 did cite I8 candidate behavior artifacts, but not the successor publications produced by that merge. The v0.2.0 migration retired those later successor pointers; none of its30 retirement targets is an ancestor of README327. README.md was also outside its changed paths. Therefore no new retirement reached this README; freshness was not evidence that its text still matched the new runtime. This correction rests on current published module, dialect, identity, projection and architecture artifacts so their next exact-path retirement can reach the documentation.

Verification: README/current-reference consistency, all8 local targets, preserved historical previousInputRuntime test, and identical shell examples pass. Full corpora were not rerun and no prose-mirroring tests were added; normal CI applies. No production source, module, identity, schema, application or service change.
