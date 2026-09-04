package recordruntime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
	jsonata "github.com/jsonata-go/jsonata/v206"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
)

func TestContinuePreservesRowsAndUpdatesQueryPolicy(t *testing.T) {
	ctx := context.Background()
	app := ledger(t)
	log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
	p := openLedger(t, app, log)
	if err := p.advanceFixture(ctx, log); err != nil {
		t.Fatal(err)
	}
	files := ledgerSources()
	files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "WRITES ledger;", "WRITES ledger, extra;", 1) + "\nCREATE TABLE extra (id TEXT NOT NULL, PRIMARY KEY(id));\nCREATE EXPORT extra_rows AS SELECT id FROM extra;")
	next, err := loadSource(files, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	p.checkpoint = func(stage string) error {
		if stage == "before-continue-commit" {
			return errors.New("injected continue failure")
		}
		return nil
	}
	if err = p.continueFixture(ctx, next); err == nil {
		t.Fatal("continue failure not observed")
	}
	if p.app.Revision() != app.Revision() {
		t.Fatal("failed continue replaced handle")
	}
	var count int
	if err = p.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='extra'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial continue schema: %d %v", count, err)
	}
	p.checkpoint = nil
	if err = p.continueFixture(ctx, next); err != nil {
		t.Fatal(err)
	}
	result, err := p.queryFixture(ctx, "SELECT qty FROM ledger")
	if err != nil || !reflect.DeepEqual(result.Rows, [][]any{{int64(2)}}) {
		t.Fatalf("prior rows lost: %#v %v", result, err)
	}
	result, err = p.queryFixture(ctx, "SELECT id FROM extra")
	if err != nil || len(result.Rows) != 0 {
		t.Fatalf("new table/query policy: %#v %v", result, err)
	}
	var revision, schema string
	if err = p.db.QueryRow("SELECT revision,storage_schema FROM gitseq_projection_identity").Scan(&revision, &schema); err != nil || revision != next.Revision() || schema != next.StorageSchemaDigest() {
		t.Fatalf("identity not advanced: %s %s %v", revision, schema, err)
	}
	changed := ledgerSources()
	changed["application.sql"].Data = []byte(strings.Replace(string(changed["application.sql"].Data), "qty INTEGER NOT NULL CHECK(qty >= 0)", "qty REAL NOT NULL CHECK(qty >= 0)", 1))
	bad, err := loadSource(changed, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.continueFixture(ctx, bad); err == nil {
		t.Fatal("incompatible continue admitted")
	}
	if p.app != next {
		t.Fatal("failed compatibility check changed handle")
	}
}

func TestReadCardinalitiesAndEmptyMany(t *testing.T) {
	ctx := context.Background()
	p := openLedger(t, ledger(t), unitLog())
	if _, err := p.db.Exec("INSERT INTO ledger VALUES ('a',1),('b',2)"); err != nil {
		t.Fatal(err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, cardinality := range []jsonataddl.Cardinality{jsonataddl.One, jsonataddl.OptionalOne} {
		if _, err := executeRead(ctx, tx, jsonataddl.Read{SQL: "SELECT id FROM ledger", Cardinality: cardinality}, map[string]any{}); err == nil {
			t.Errorf("%s accepted multiple rows", cardinality)
		}
	}
	value, err := executeRead(ctx, tx, jsonataddl.Read{SQL: "SELECT id FROM ledger WHERE id='missing' LIMIT 4", Cardinality: jsonataddl.Many, Limit: 4}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if string(encoded) != "[]" {
		t.Fatalf("empty MANY = %s", encoded)
	}
	if _, err := executeRead(ctx, tx, jsonataddl.Read{SQL: "SELECT id FROM ledger WHERE id='missing'", Cardinality: jsonataddl.One}, map[string]any{}); err == nil {
		t.Fatal("ONE accepted zero rows")
	}
}

func TestTimeoutRollsBackWithoutDeterministicGap(t *testing.T) {
	log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
	p := openLedger(t, ledger(t), log)
	p.checkpoint = func(stage string) error {
		if stage == "after-changes" {
			return &jsonata.JSONataError{Code: "U1002"}
		}
		return nil
	}
	err := p.advanceFixture(context.Background(), log)
	if !jsonataddl.IsEvaluationTimeout(err) {
		t.Fatalf("timeout identity lost: %v", err)
	}
	if p.frontier.InterpretedPosition != 0 || p.frontier.GapEvent != "" {
		t.Fatalf("timeout persisted deterministic gap: %#v", p.frontier)
	}
	var count int
	if err := p.db.QueryRow("SELECT count(*) FROM ledger").Scan(&count); err != nil || count != 0 {
		t.Fatalf("timeout wrote rows: %d %v", count, err)
	}
	p.checkpoint = nil
	if err = p.advanceFixture(context.Background(), log); err != nil {
		t.Fatal(err)
	}
}

func TestQueryCodecUsesCoreLogicalValues(t *testing.T) {
	for _, test := range []struct {
		value    any
		declared string
		want     any
	}{
		{nil, "TEXT", nil}, {int64(1), "BOOLEAN", true}, {int64(2), "INTEGER", int64(2)},
		{int64(9007199254740992), "INTEGER", jsonataddl.WrapInteger(9007199254740992)},
		{[]byte{0, 255}, "BLOB", jsonataddl.WrapBytes([]byte{0, 255})},
		{`{"ok":true}`, "JSON", map[string]any{"ok": true}},
	} {
		got, err := queryValue(test.value, test.declared)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("queryValue(%v,%s)=%#v,%v want %#v", test.value, test.declared, got, err, test.want)
		}
	}
	if _, err := queryValue(math.Inf(1), "REAL"); err == nil {
		t.Fatal("nonfinite number admitted")
	}
}

func TestTypedValuesCrossStorageReadAndQuery(t *testing.T) {
	for _, document := range []struct {
		name         string
		input, query any
	}{
		{"object", map[string]any{"number": json.Number("42")}, map[string]any{"number": json.Number("42")}},
		{"scalar", json.Number("42"), json.Number("42")},
	} {
		t.Run(document.name, func(t *testing.T) {
			ctx := context.Background()
			files := ledgerSources()
			files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "WRITES ledger;", "WRITES ledger, typed;", 1) + `
CREATE TABLE typed (id TEXT NOT NULL, flag BOOLEAN NOT NULL, document JSON NOT NULL, bytes BLOB NOT NULL, number INTEGER NOT NULL, PRIMARY KEY(id));`)
			app, err := loadSource(files, "adapter-fixture")
			if err != nil {
				t.Fatal(err)
			}
			p := openLedger(t, app, unitLog())
			tx, err := p.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			written := map[string]any{"id": "a", "flag": true, "document": document.input, "bytes": "AP8=", "number": json.Number("9007199254740991")}
			if err = writeRow(ctx, tx, app.Tables()["typed"], "insert", written); err != nil {
				t.Fatal(err)
			}
			read := jsonataddl.Read{SQL: "SELECT id,flag,document,bytes,number FROM typed WHERE id=?", Parameters: []string{"id"}, Cardinality: jsonataddl.One}
			got, err := executeRead(ctx, tx, read, map[string]any{"id": "a"})
			wantRead := map[string]any{"id": "a", "flag": true, "document": document.input, "bytes": "AP8=", "number": int64(9007199254740991)}
			if err != nil || !reflect.DeepEqual(got, wantRead) {
				t.Fatalf("fold read=%#v %v; want %#v", got, err, wantRead)
			}
			var flagClass, jsonClass, blobClass, numberClass string
			if err = tx.QueryRow("SELECT typeof(flag),typeof(document),typeof(bytes),typeof(number) FROM typed").Scan(&flagClass, &jsonClass, &blobClass, &numberClass); err != nil {
				t.Fatal(err)
			}
			if classes := []string{flagClass, jsonClass, blobClass, numberClass}; !reflect.DeepEqual(classes, []string{"integer", "text", "blob", "integer"}) {
				t.Fatalf("wrong storage classes: %v", classes)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			result, err := p.queryFixture(ctx, "SELECT id,flag,document,bytes,number FROM typed")
			wantQuery := [][]any{{"a", true, document.query, jsonataddl.WrapBytes([]byte{0, 255}), int64(9007199254740991)}}
			if err != nil || !reflect.DeepEqual(result.Rows, wantQuery) {
				t.Fatalf("query=%#v %v; want %#v", result.Rows, err, wantQuery)
			}
		})
	}
}

func TestUnownedSQLiteJournalModeIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unowned.sqlite")
	db, err := sqlitedriver.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE valuable (value TEXT)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := openFixture(context.Background(), path, ledger(t), unitLog()); err == nil {
		p.close()
		t.Fatal("unowned SQLite admitted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("opening an unrelated database changed its bytes")
	}
}

func TestLegacyJSONAffinityRefusesContinueAndAutomaticReset(t *testing.T) {
	ctx := context.Background()
	files := ledgerSources()
	files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "WRITES ledger;", "WRITES ledger, typed;", 1) + `
CREATE TABLE typed (id TEXT NOT NULL, document JSON NOT NULL, PRIMARY KEY(id));`)
	app, err := loadSource(files, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	log := unitLog()
	p, err := openFixture(ctx, path, app, log)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	// Retain the current compiled identity to exercise actual storage, not
	// merely a version-string mismatch. ddl/1 coerced a JSON number to INTEGER.
	if _, err := p.db.Exec(`DROP TABLE typed; CREATE TABLE typed (id TEXT NOT NULL, document JSON NOT NULL, PRIMARY KEY(id)); INSERT INTO typed VALUES ('saved','42')`); err != nil {
		t.Fatal(err)
	}
	beforeApp, beforeFrontier := p.app, p.frontier
	if err := p.continueFixture(ctx, app); err == nil || !strings.Contains(err.Error(), "legacy JSON-affinity") {
		t.Fatalf("unsafe continuation: %v", err)
	}
	if p.app != beforeApp || p.frontier != beforeFrontier {
		t.Fatal("failed continuation changed handle/frontier")
	}
	var count int
	if err := p.db.QueryRow(`SELECT count(*) FROM typed WHERE id='saved' AND typeof(document)='integer'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy row changed: %d %v", count, err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"adapter-fixture", "changed-application"} {
		next, err := loadSource(files, name)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := openFixture(ctx, path, next, log)
		if err == nil {
			opened.close()
			t.Fatal("legacy JSON storage was reused or automatically reset")
		}
		if !strings.Contains(err.Error(), "legacy JSON-affinity") {
			t.Fatal(err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("refused open changed stored bytes")
		}
	}
}

func TestPrimaryKeySpellingAndDeleteProvenance(t *testing.T) {
	ctx := context.Background()
	files := ledgerSources()
	files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "PRIMARY KEY(id)", "PRIMARY KEY(ID)", 1))
	app, err := loadSource(files, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
	p := openLedger(t, app, log)
	if err = p.advanceFixture(ctx, log); err != nil {
		t.Fatal(err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	changes := map[string]jsonataddl.TableChanges{"ledger": {Delete: []map[string]any{{"id": "a"}}}}
	if err = p.apply(ctx, tx, changes, host.Record{ID: "deletion"}, 2); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count, version int
	var key, operation string
	if err = p.db.QueryRow("SELECT count(*) FROM ledger").Scan(&count); err != nil || count != 0 {
		t.Fatalf("delete failed: %d %v", count, err)
	}
	if err = p.db.QueryRow("SELECT key_json,operation FROM gitseq_row_provenance WHERE event_id='deletion'").Scan(&key, &operation); err != nil || key != `{"id":"a"}` || operation != "delete" {
		t.Fatalf("provenance=%s %s %v", key, operation, err)
	}
	if err = p.db.QueryRow("SELECT version FROM __gitseq_row_versions").Scan(&version); err != nil || version != 2 {
		t.Fatalf("row version=%d %v", version, err)
	}
}
