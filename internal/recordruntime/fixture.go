package recordruntime

import (
	"bytes"
	"context"
	"errors"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

// Fixture exposes only completed demonstration reads, never incremental
// interpretation or activation of another application.
type Fixture struct{ projection *projection }

// admitFixture is deliberately a closed shape, not a caller-supplied flag.
// Record IDs, signed timestamps and actor keys may vary between generated
// examples; the interpreted schemas, payload bytes and causal lists may not.
func admitFixture(log host.Log) error {
	refuse := errors.New("fixture-only runtime: expected the sealed inventory-fixture example; production replay requires deterministic step and allocation bounds")
	if log.Depth != 4 || len(log.Records) != 4 || log.Genesis == "" || log.Head == "" {
		return refuse
	}
	if log.Records[0].Schema != "gitseq/app-binding@0" {
		return refuse
	}
	for index, want := range []struct{ schema, payload string }{
		{"stock_received", `{"id":"stock-1","qty":5,"sku":"ink"}`},
		{"reservation_requested", `{"id":"reservation-1","qty":2,"sku":"ink"}`},
		{"reservation_requested", `{"id":"reservation-2","qty":4,"sku":"ink"}`},
	} {
		record := log.Records[index+1]
		if record.Schema != want.schema || !bytes.Equal(record.Payload, []byte(want.payload)) || len(record.RestsOn) != 0 || record.Actor != log.Records[0].Actor {
			return refuse
		}
	}
	return nil
}

// BuildFixture requires a log already verified against the current binding by
// the public host. It admits the closed demonstration before invoking the
// runtime or touching a database. Broader replay stays private to corpus tests.
func BuildFixture(ctx context.Context, app *jsonataddl.Application, log host.Log, path string) (*Fixture, error) {
	if err := admitFixture(log); err != nil {
		return nil, err
	}
	p, err := openFixture(ctx, path, app, log)
	if err != nil {
		return nil, err
	}
	if err := p.advanceFixture(ctx, log); err != nil {
		return nil, errors.Join(err, p.close())
	}
	return &Fixture{projection: p}, nil
}

// Query runs one bounded read-only SELECT against the completed fixture.
func (f *Fixture) Query(ctx context.Context, sql string) (QueryResult, error) {
	return f.projection.queryFixture(ctx, sql)
}

// Close releases both SQLite connections.
func (f *Fixture) Close() error { return f.projection.close() }
