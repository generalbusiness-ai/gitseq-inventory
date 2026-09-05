// Package recordruntime defines the inventory host's fixture-only runtime.
// It delegates compilation, evaluation and logical values to jsonataddl.
package recordruntime

import "github.com/generalbusiness-ai/tailapps/jsonataddl"

// GitseqRecord returns the reviewed dialect for verified Gitseq records.
// These bounds constrain values and source sizes; they do not provide
// deterministic evaluator step or allocation bounds. Production replay is
// therefore not authorized. See docs/reference/architecture.md.
func GitseqRecord() jsonataddl.Dialect {
	return jsonataddl.Dialect{
		Identity: jsonataddl.DialectIdentity{Name: "gitseq-record", Version: "1"},
		Layout: jsonataddl.SourceLayout{
			DefinitionPath: "application.sql", ProgramRoot: "folds", ProgramSuffix: ".jsonata",
		},
		HostEvent: jsonataddl.NewEventContract("gitseq_record",
			jsonataddl.EnvelopeField{Name: "id", Type: "TEXT"},
			jsonataddl.EnvelopeField{Name: "schema", Type: "TEXT"},
			jsonataddl.EnvelopeField{Name: "actor", Type: "TEXT"},
			jsonataddl.EnvelopeField{Name: "position", Type: "TEXT"},
			jsonataddl.EnvelopeField{Name: "timestamp", Type: "TEXT"},
			jsonataddl.EnvelopeField{Name: "payload_digest", Type: "TEXT"},
		),
		PrivateEvent: jsonataddl.PrivateEventPolicy{Name: "inventory_event", ExactlyOne: true},
		Topology:     jsonataddl.TopologyPolicy{ExactlyOneNormalizer: true, AtLeastOneFold: true},
		Authority: jsonataddl.AuthorityPolicy{
			NormalizerReads:    jsonataddl.ReadOwnTables,
			FoldReads:          jsonataddl.ReadOwnAndNormalizerTables,
			SingleWriterTables: true,
		},
		Limits: jsonataddl.Limits{
			MaxElementBytes: 16 << 10,
			MaxSourceBytes:  64 << 10,
			MaxProgramBytes: 16 << 10,
			MaxInputBytes:   32 << 10,
			MaxOutputBytes:  16 << 10,
			MaxDepth:        16,
			MaxRange:        64,
			MaxEvents:       1,
			MaxFacts:        8,
			MaxRowChanges:   8,
			MaxManyRows:     64,
		},
	}
}

func components() []jsonataddl.Component {
	return append(jsonataddl.CoreComponents(),
		jsonataddl.DialectComponent(GitseqRecord()),
		jsonataddl.Component{Key: "host.canonicalization", Value: "gitseq-record/2"},
		jsonataddl.Component{Key: "host.orchestration", Value: "one-record-txn/1"},
		jsonataddl.Component{Key: "host.projection", Value: "gitseq-query-values/1"},
	)
}

// Identity composes the five upstream components, the complete dialect and
// the three host contracts. The fixture binding and projection both record it.
func Identity() (jsonataddl.RuntimeIdentity, error) {
	return jsonataddl.ComposeIdentity(components()...)
}
