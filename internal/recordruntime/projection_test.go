package recordruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

func ledgerSources() fstest.MapFS {
	return fstest.MapFS{
		"application.sql": {Data: []byte(`CREATE EVENT inventory_event (id TEXT NOT NULL, qty INTEGER NOT NULL);
CREATE TABLE ledger (id TEXT NOT NULL, qty INTEGER NOT NULL CHECK(qty >= 0), PRIMARY KEY(id));
CREATE NORMALIZER normalize ON gitseq_record USING 'folds/normalize.jsonata' EMITS inventory_event;
CREATE FOLD apply ON inventory_event
READ prior OPTIONAL ONE AS SELECT id,qty FROM ledger WHERE id=:event.id
USING 'folds/apply.jsonata' WRITES ledger;
CREATE EXPORT ledger_rows AS SELECT id,qty FROM ledger;`)},
		"folds/normalize.jsonata": {Data: []byte(`event.schema = "stock_received" or event.schema = "reservation_requested" ?
{"decision":"effective","facts":[],"tables":{},"events":{"inventory_event":[{"id":event.payload.id,"qty":event.payload.qty}]}} :
{"decision":"effective","facts":[],"tables":{}}`)},
		"folds/apply.jsonata": {Data: []byte(`event.id = "refused" ?
{"decision":"ineffective","facts":[{"kind":"refused-fixture"}],"tables":{}} :
{"decision":"effective","facts":[],"tables":{"ledger":{"upsert":[{"id":event.id,"qty":event.qty+(rows.prior ? rows.prior.qty : 0)}]}}}`)},
	}
}

func ledger(t *testing.T) *jsonataddl.Application {
	t.Helper()
	app, err := LoadSource(ledgerSources(), "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func unitLog(payloads ...string) host.Log {
	log := host.Log{Genesis: "unit-genesis", Head: fmt.Sprintf("head-%d", len(payloads)), Depth: len(payloads)}
	for i, payload := range payloads {
		log.Records = append(log.Records, host.Record{ID: fmt.Sprintf("event-%d", i+1), Actor: "unit-actor", Schema: "stock_received", Payload: []byte(payload), Timestamp: 1000 + int64(i)})
	}
	return log
}

func openLedger(t *testing.T, app *jsonataddl.Application, log host.Log) *projection {
	t.Helper()
	p, err := openFixture(context.Background(), filepath.Join(t.TempDir(), "fixture.sqlite"), app, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func TestVerifiedFixtureReplayAndReuse(t *testing.T) {
	ctx := context.Background()
	app := ledger(t)
	identity, err := Identity()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "records")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := host.Application{Name: app.Name(), FoldVersion: identity.Digest()}
	workspace, err := host.Init(ctx, repo, binding, key, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, payload := range []string{`{"id":"a","qty":2,"sku":"ink"}`, `{"id":"a","qty":3,"sku":"ink"}`, `{"id":"refused","qty":1,"sku":"ink"}`} {
		if _, err := workspace.Append(ctx, key, host.Act{Schema: "stock_received", Payload: []byte(payload), IdempotencyKey: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err = host.Open(ctx, repo, binding)
	if err != nil {
		t.Fatal(err)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	p, err := openFixture(ctx, path, app, log)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.advanceFixture(ctx, log); err != nil {
		p.close()
		t.Fatal(err)
	}
	result, err := p.queryFixture(ctx, "SELECT qty FROM ledger WHERE id='a'")
	if err != nil {
		p.close()
		t.Fatal(err)
	}
	if !result.Frontier.Complete || !reflect.DeepEqual(result.Rows, [][]any{{int64(5)}}) {
		t.Errorf("result=%#v", result)
	}
	decisions, err := p.queryFixture(ctx, "SELECT position,decision FROM gitseq_decisions ORDER BY position")
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]any{{int64(2), "effective"}, {int64(3), "effective"}, {int64(4), "ineffective"}}; !reflect.DeepEqual(decisions.Rows, want) {
		t.Errorf("decisions=%#v", decisions.Rows)
	}
	if err = p.close(); err != nil {
		t.Fatal(err)
	}
	p, err = openFixture(ctx, path, app, log)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	p.checkpoint = func(string) error { return errors.New("should reuse without evaluation") }
	if err = p.advanceFixture(ctx, log); err != nil {
		t.Fatalf("cache was not reused: %v", err)
	}
	if p.frontier.InterpretedPosition != 4 {
		t.Fatal(p.frontier)
	}
}

func TestRecordFailureRollsBackEveryRelation(t *testing.T) {
	for _, stage := range []string{"after-changes", "before-frontier", "before-commit"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
			p := openLedger(t, ledger(t), log)
			p.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("injected storage failure")
				}
				return nil
			}
			if err := p.advanceFixture(ctx, log); err == nil {
				t.Fatal("failure injection did not fire")
			}
			for _, table := range []string{"ledger", "gitseq_decisions", "gitseq_facts", "gitseq_row_provenance", "__gitseq_row_versions"} {
				var n int
				if err := p.db.QueryRow("SELECT count(*) FROM " + quote(table)).Scan(&n); err != nil || n != 0 {
					t.Errorf("%s has %d rows: %v", table, n, err)
				}
			}
			if p.frontier.InterpretedPosition != 0 || p.frontier.GapEvent != "event-1" {
				t.Fatal(p.frontier)
			}
			p.checkpoint = nil
			if err := p.advanceFixture(ctx, log); err != nil {
				t.Fatal(err)
			}
			if !p.frontier.Complete || p.frontier.GapEvent != "" {
				t.Fatal(p.frontier)
			}
		})
	}
}

func TestMalformedPayloadAndConstraintFailure(t *testing.T) {
	for _, payload := range []string{`{"id":"a"}`, `{"id":"a","qty":"two","sku":"ink"}`, `{"id":"a","qty":-1,"sku":"ink"}`, `{"extra":true,"id":"a","qty":2,"sku":"ink"}`, `{ "id":"a", "qty":2 }`, `[]`, `{} {}`, `{"x":"` + strings.Repeat("x", maxPayloadBytes) + `"}`} {
		t.Run(payload[:min(30, len(payload))], func(t *testing.T) {
			log := unitLog(payload)
			p := openLedger(t, ledger(t), log)
			if err := p.advanceFixture(context.Background(), log); err == nil {
				t.Fatal("invalid payload interpreted")
			}
			if p.frontier.InterpretedPosition != 0 {
				t.Fatal(p.frontier)
			}
			var count int
			if err := p.db.QueryRow("SELECT count(*) FROM gitseq_decisions").Scan(&count); err != nil || count != 0 {
				t.Fatalf("decision created: %d, %v", count, err)
			}
		})
	}
}

func TestEachCacheIdentityMismatchDiscardsRows(t *testing.T) {
	for _, column := range []string{"genesis", "application", "revision", "storage_schema"} {
		t.Run(column, func(t *testing.T) {
			ctx := context.Background()
			app := ledger(t)
			log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
			path := filepath.Join(t.TempDir(), "cache.sqlite")
			p, err := openFixture(ctx, path, app, log)
			if err != nil {
				t.Fatal(err)
			}
			if err = p.advanceFixture(ctx, log); err != nil {
				p.close()
				t.Fatal(err)
			}
			if _, err = p.db.Exec("UPDATE gitseq_projection_identity SET " + quote(column) + "='different'"); err != nil {
				p.close()
				t.Fatal(err)
			}
			if err = p.close(); err != nil {
				t.Fatal(err)
			}
			p, err = openFixture(ctx, path, app, log)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			if p.frontier.InterpretedPosition != 0 {
				t.Fatal("mismatched cache reused")
			}
			if err = p.advanceFixture(ctx, log); err != nil {
				t.Fatal(err)
			}
			result, err := p.queryFixture(ctx, "SELECT qty FROM ledger")
			if err != nil || !reflect.DeepEqual(result.Rows, [][]any{{int64(2)}}) {
				t.Fatalf("reset replay=%#v %v", result, err)
			}
		})
	}
}

func TestAuthorizerReleaseAndQueryIsolation(t *testing.T) {
	ctx := context.Background()
	app := ledger(t)
	p := openLedger(t, app, unitLog(`{"id":"a","qty":2,"sku":"ink"}`))
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readInput(ctx, tx, app, p.guard, "apply", map[string]any{}); err == nil {
		t.Fatal("missing read parameter admitted")
	}
	if p.guard.seated.Load() != nil {
		t.Fatal("authorizer left seated after failure")
	}
	guard, ok := app.ReadAuthorizer("apply")
	if !ok {
		t.Fatal("no authorizer")
	}
	p.guard.seated.Store(&guard)
	for _, statement := range []string{"SELECT * FROM gitseq_frontier", "DELETE FROM ledger", "SELECT random()", "PRAGMA user_version"} {
		if _, err = tx.ExecContext(ctx, statement); err == nil {
			t.Errorf("fold admitted %s", statement)
		}
	}
	p.guard.seated.Store(nil)
	if _, err = tx.ExecContext(ctx, "UPDATE gitseq_frontier SET verified_depth=0"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"UPDATE ledger SET qty=0", "SELECT random()", "SELECT * FROM __gitseq_row_versions", "SELECT * FROM sqlite_master", "SELECT 1; DELETE FROM ledger", "PRAGMA table_info(ledger)"} {
		if _, err = p.queryFixture(ctx, statement); err == nil {
			t.Errorf("query admitted %s", statement)
		}
	}
}

func TestRecordInputOwnsCausalSliceAndPreservesNumbers(t *testing.T) {
	record := host.Record{ID: "id", Schema: "stock_received", Actor: "actor", Timestamp: -7, Payload: []byte(`{"id":"a","qty":9007199254740991,"sku":"ink"}`), RestsOn: []string{"second", "first", "second"}}
	input, err := recordInput(record, 1)
	if err != nil {
		t.Fatal(err)
	}
	if input.Event["position"] != "1" || input.Event["timestamp"] != "-7" || input.Event["schema"] != "stock_received" {
		t.Fatal(input)
	}
	if _, ok := input.Event["payload"].(map[string]any)["qty"].(json.Number); !ok {
		t.Fatal("number lost precision")
	}
	input.Event["rests_on"].([]string)[0] = "changed"
	if record.RestsOn[0] != "second" {
		t.Fatal("borrowed record mutated")
	}
	if input.Meta == nil || input.Rows == nil {
		t.Fatal("null empty objects")
	}
}

func TestUnownedDatabaseIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unowned.sqlite")
	if err := os.WriteFile(path, []byte("not a projection"), 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := openFixture(context.Background(), path, ledger(t), unitLog()); err == nil {
		p.close()
		t.Fatal("unowned file admitted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "not a projection" {
		t.Fatalf("unowned file changed: %q %v", data, err)
	}
}
