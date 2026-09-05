package recordruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
)

func TestAdmissionRejectsMalformedRecognizedRecords(t *testing.T) {
	for name, payload := range map[string]string{
		"empty": "", "scalar": `42`, "array": `[]`, "null": `null`,
		"duplicate":     `{"id":"a","id":"b","qty":1,"sku":"ink"}`,
		"trailing":      `{"id":"a","qty":1,"sku":"ink"} {}`,
		"whitespace":    `{ "id":"a","qty":1,"sku":"ink"}`,
		"order":         `{"sku":"ink","id":"a","qty":1}`,
		"extra":         `{"extra":true,"id":"a","qty":1,"sku":"ink"}`,
		"missing":       `{"id":"a","qty":1}`,
		"wrong-name":    `{"id":"a","other":"ink","qty":1}`,
		"id-type":       `{"id":2,"qty":1,"sku":"ink"}`,
		"sku-null":      `{"id":"a","qty":1,"sku":null}`,
		"quantity-type": `{"id":"a","qty":"1","sku":"ink"}`,
		"fraction":      `{"id":"a","qty":1.5,"sku":"ink"}`,
		"zero":          `{"id":"a","qty":0,"sku":"ink"}`,
		"negative":      `{"id":"a","qty":-1,"sku":"ink"}`,
		"inexact":       `{"id":"a","qty":9007199254740992,"sku":"ink"}`,
		"oversize":      `{"id":"a","qty":1,"sku":"` + strings.Repeat("x", maxPayloadBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			for _, schema := range []string{"stock_received", "reservation_requested"} {
				log := unitLog(payload)
				log.Records[0].Schema = schema
				p := openLedger(t, ledger(t), log)
				if err := p.advanceFixture(context.Background(), log); err == nil {
					t.Fatal("malformed recognized record was admitted")
				}
				if p.frontier.InterpretedPosition != 0 || p.frontier.GapEvent != log.Records[0].ID {
					t.Fatal("malformed record advanced the interpreted frontier")
				}
				for _, table := range []string{"ledger", "gitseq_decisions", "gitseq_facts", "gitseq_row_provenance", "__gitseq_row_versions"} {
					var count int
					if err := p.db.QueryRow("SELECT count(*) FROM " + quote(table)).Scan(&count); err != nil || count != 0 {
						t.Fatalf("malformed record changed %s: %d %v", table, count, err)
					}
				}
			}
		})
	}
}

func TestUnknownSchemaSkipsBeforePayloadDecoding(t *testing.T) {
	for _, payload := range []string{"", "invalid", "42", "null", strings.Repeat("x", maxPayloadBytes+1)} {
		for _, schema := range []string{"unrelated", "Stock_received"} {
			log := unitLog(payload)
			log.Records[0].Schema = schema
			p := openLedger(t, ledger(t), log)
			if err := p.advanceFixture(context.Background(), log); err != nil || !p.frontier.Complete {
				t.Fatalf("unknown record was decoded/refused: %s %v", schema, err)
			}
			for _, table := range []string{"ledger", "gitseq_decisions", "gitseq_facts"} {
				var count int
				if err := p.db.QueryRow("SELECT count(*) FROM " + quote(table)).Scan(&count); err != nil || count != 0 {
					t.Fatalf("skip wrote %s: %d %v", table, count, err)
				}
			}
		}
	}
}

func TestAdmissionValidBoundaries(t *testing.T) {
	object := map[string]any{"id": "", "qty": json.Number("9007199254740991"), "sku": ""}
	encoded, _ := json.Marshal(object)
	object["sku"] = strings.Repeat("x", maxPayloadBytes-len(encoded))
	encoded, _ = json.Marshal(object)
	if len(encoded) != maxPayloadBytes {
		t.Fatal("fixture did not reach the exact payload bound")
	}
	input, err := recordInput(host.Record{Schema: "stock_received", Payload: encoded}, 1)
	if err != nil || input.Event["payload"].(map[string]any)["qty"] != json.Number("9007199254740991") {
		t.Fatalf("valid boundary rejected or rounded: %#v %v", input, err)
	}
}

func TestRecognizedRecordCannotDisappearAtNormalizer(t *testing.T) {
	files := ledgerSources()
	files["folds/normalize.jsonata"].Data = []byte(`{"decision":"effective","facts":[],"tables":{}}`)
	app, err := LoadSource(files, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	log := unitLog(`{"id":"a","qty":1,"sku":"ink"}`)
	p := openLedger(t, app, log)
	if err := p.advanceFixture(context.Background(), log); err == nil || p.frontier.InterpretedPosition != 0 {
		t.Fatal("recognized record became a silent skip")
	}
}
