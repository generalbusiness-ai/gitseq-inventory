package recordruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

func completeRecordInput(t *testing.T) jsonataddl.EvaluationInput {
	t.Helper()
	input, err := recordInput(unitLog(`{"id":"a","qty":1,"sku":"ink"}`).Records[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// These direct callers bypass host record admission deliberately. The actual
// Inventory dialect must bind the whole frozen input even when a program does
// not inspect a malformed member.
func TestDeclaredRecordInput(t *testing.T) {
	app := ledger(t)
	changes := map[string]func(*jsonataddl.EvaluationInput){
		"missing meta":        func(i *jsonataddl.EvaluationInput) { i.Meta = nil },
		"extra meta":          func(i *jsonataddl.EvaluationInput) { i.Meta["extra"] = true },
		"null event":          func(i *jsonataddl.EvaluationInput) { i.Event = nil },
		"extra event":         func(i *jsonataddl.EvaluationInput) { i.Event["extra"] = true },
		"null rows":           func(i *jsonataddl.EvaluationInput) { i.Rows = nil },
		"extra rows":          func(i *jsonataddl.EvaluationInput) { i.Rows["extra"] = nil },
		"unordered shape":     func(i *jsonataddl.EvaluationInput) { i.Event["rests_on"] = "a" },
		"nontext causal":      func(i *jsonataddl.EvaluationInput) { i.Event["rests_on"] = []any{"a", 2} },
		"extra payload":       func(i *jsonataddl.EvaluationInput) { i.Event["payload"].(map[string]any)["extra"] = true },
		"missing payload qty": func(i *jsonataddl.EvaluationInput) { delete(i.Event["payload"].(map[string]any), "qty") },
		"fractional qty":      func(i *jsonataddl.EvaluationInput) { i.Event["payload"].(map[string]any)["qty"] = json.Number("1.5") },
		"inexact qty": func(i *jsonataddl.EvaluationInput) {
			i.Event["payload"].(map[string]any)["qty"] = json.Number("9007199254740992")
		},
		"null payload sku":   func(i *jsonataddl.EvaluationInput) { i.Event["payload"].(map[string]any)["sku"] = nil },
		"numeric payload id": func(i *jsonataddl.EvaluationInput) { i.Event["payload"].(map[string]any)["id"] = 7 },
	}
	for _, name := range []string{"id", "schema", "actor", "position", "timestamp", "payload_digest", "rests_on", "payload"} {
		changes["missing "+name] = func(i *jsonataddl.EvaluationInput) { delete(i.Event, name) }
		changes["null "+name] = func(i *jsonataddl.EvaluationInput) { i.Event[name] = nil }
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			input := completeRecordInput(t)
			change(&input)
			if _, err := app.Evaluate("normalize", input); err == nil || !strings.Contains(err.Error(), "input") {
				t.Fatalf("malformed input was not refused by the input boundary: %v", err)
			}
		})
	}
	for _, chain := range [][]string{{}, {"b", "a"}, {"b", "b"}} {
		input := completeRecordInput(t)
		input.Event["rests_on"] = chain
		if _, err := app.Evaluate("normalize", input); err != nil {
			t.Fatal(err)
		}
	}
	input := completeRecordInput(t)
	input.Event["payload"].(map[string]any)["qty"] = json.Number("9007199254740991")
	if _, err := app.Evaluate("normalize", input); err != nil {
		t.Fatal(err)
	}
}

func TestInputValidationPrecedesReads(t *testing.T) {
	ctx := context.Background()
	app := ledger(t)
	p := openLedger(t, app, unitLog())
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A closed transaction makes the ordering observable. Bad input must fail
	// before touching it; valid input must reach the actual read and ErrTxDone.
	if _, err := readInput(ctx, tx, app, p.guard, "apply", map[string]any{"id": "a", "qty": "bad"}); err == nil || !strings.Contains(err.Error(), "input event") {
		t.Fatalf("pre-read boundary absent: %v", err)
	}
	if _, err := readInput(ctx, tx, app, p.guard, "apply", map[string]any{"id": "a", "qty": 1}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("valid control did not reach read: %v", err)
	}
	if p.guard.seated.Load() != nil {
		t.Fatal("read authorizer left seated")
	}
}

func TestDeclaredAnalyticInputAndRows(t *testing.T) {
	app := ledger(t)
	for name, rows := range map[string]map[string]any{
		"missing read": {}, "extra read": {"prior": nil, "extra": nil},
		"array for optional one": {"prior": []any{}},
		"missing column":         {"prior": map[string]any{"id": "a"}},
		"extra column":           {"prior": map[string]any{"id": "a", "qty": 1, "extra": true}},
		"wrong type":             {"prior": map[string]any{"id": "a", "qty": "1"}},
		"null required column":   {"prior": map[string]any{"id": "a", "qty": nil}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := app.Evaluate("apply", jsonataddl.EvaluationInput{Meta: map[string]any{}, Event: map[string]any{"id": "a", "qty": 1}, Rows: rows})
			if err == nil || !strings.Contains(err.Error(), "input rows") {
				t.Fatalf("read contract not enforced: %v", err)
			}
		})
	}
	for _, prior := range []any{nil, map[string]any{"id": "a", "qty": 1}} {
		if _, err := app.Evaluate("apply", jsonataddl.EvaluationInput{Meta: map[string]any{}, Event: map[string]any{"id": "a", "qty": 1}, Rows: map[string]any{"prior": prior}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, meta := range []map[string]any{nil, {"extra": true}} {
		if _, err := app.Evaluate("apply", jsonataddl.EvaluationInput{Meta: meta, Event: map[string]any{"id": "a", "qty": 1}, Rows: map[string]any{"prior": nil}}); err == nil {
			t.Fatal("analytic metadata contract ignored")
		}
	}
}

func TestDeclaredOneAndManyRows(t *testing.T) {
	for _, cardinality := range []string{"ONE", "MANY LIMIT 2"} {
		t.Run(cardinality, func(t *testing.T) {
			files := ledgerSources()
			files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "READ prior OPTIONAL ONE", "READ prior "+cardinality, 1))
			if cardinality == "MANY LIMIT 2" {
				files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "WHERE id=:event.id", "WHERE id=:event.id ORDER BY id", 1))
			}
			// The program ignores rows, so only the declared read contract can reject
			// a malformed result. The same compiled read is exercised below via SQL.
			files["folds/apply.jsonata"].Data = []byte(`{"decision":"effective","facts":[],"tables":{}}`)
			app, err := LoadSource(files, "adapter-fixture")
			if err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": "a", "qty": 1}
			good, bad := []any{row}, []any{nil, []any{}, []any{row}}
			if cardinality == "MANY LIMIT 2" {
				good = []any{[]any{}, []any{row}, []any{row, row}}
				bad = []any{nil, row, []any{row, row, row}, []any{map[string]any{"id": "a", "qty": "bad"}}}
			}
			for _, value := range good {
				if _, err := app.Evaluate("apply", jsonataddl.EvaluationInput{Meta: map[string]any{}, Event: row, Rows: map[string]any{"prior": value}}); err != nil {
					t.Fatalf("valid read: %v", err)
				}
			}
			for _, value := range bad {
				if _, err := app.Evaluate("apply", jsonataddl.EvaluationInput{Meta: map[string]any{}, Event: row, Rows: map[string]any{"prior": value}}); err == nil || !strings.Contains(err.Error(), "input rows") {
					t.Fatalf("invalid read admitted: %v", err)
				}
			}
			ctx := context.Background()
			p := openLedger(t, app, unitLog())
			for _, populated := range []bool{false, true} {
				if populated {
					if _, err := p.db.Exec("INSERT INTO ledger VALUES ('a',1)"); err != nil {
						t.Fatal(err)
					}
				}
				tx, err := p.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				input, err := readInput(ctx, tx, app, p.guard, "apply", row)
				if !populated && cardinality == "ONE" {
					if err == nil {
						t.Fatal("empty ONE read succeeded")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if _, err := app.Evaluate("apply", input); err != nil {
						t.Fatalf("actual SQL read violated contract: %v", err)
					}
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestInputDepthBoundIsDeclaredSeparately(t *testing.T) {
	input := completeRecordInput(t)
	var nested any = "leaf"
	for range 1024 {
		nested = []any{nested}
	}
	// The depth bound runs before the closed-shape walk. This deliberately
	// invalid payload cannot make an excessive recursive shape reach JSONata.
	input.Event["payload"] = map[string]any{"nested": nested}
	if _, err := ledger(t).Evaluate("normalize", input); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("input depth not enforced before shape: %v", err)
	}
}
