package recordruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
)

const corpusGenesis = "1e646a0c244cb07748e54f252ad92800fabd1996"
const corpusHead = "94eb93c7313b90d7808cb5db901aed3066a4c0c8"

var corpusQueries = []struct{ Name, SQL string }{
	{"decisions", "SELECT position,event_id,event_type,decision FROM gitseq_decisions ORDER BY position"},
	{"facts", "SELECT event_id,ordinal,kind,fact_json FROM gitseq_facts ORDER BY position,ordinal"},
	{"stock", "SELECT sku,available FROM stock ORDER BY sku"},
	{"reservations", "SELECT id,sku,qty FROM reservations ORDER BY id"},
}

func sealedCorpus(t *testing.T) (string, host.Log) {
	t.Helper()
	bundle, err := filepath.Abs("testdata/inventory.bundle")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != "01fd236ddab6f47db1c5a97bcbf716ad6ce02fee03de94baf05d5cce8084149d" {
		t.Fatal("sealed fixture changed")
	}
	repo := filepath.Join(t.TempDir(), "log")
	ref := "refs/seq/" + corpusGenesis
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "fetch", "-q", bundle, ref + ":" + ref}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// This older public host reads local locator metadata even for verification.
	// No sequencer or actor private key accompanies this read-only fixture.
	meta := filepath.Join(repo, ".git", "gitseq")
	if err := os.MkdirAll(meta, 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"version":0,"genesis":"` + corpusGenesis + `","object_format":"sha1","payload_ceiling":1048576,"idempotency_namespace":"jsonata-ddl-inventory-spike","sequencer_key":"no-fixture-signing-key"}`
	if err := os.WriteFile(filepath.Join(meta, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	// This is the old binding in the sealed fixture, not a migration or an
	// application activation. Both evaluators receive these same verified acts.
	w, err := host.Open(context.Background(), repo, host.Application{Name: "jsonata-ddl-inventory-spike", FoldVersion: "jsonata-v206-sqlite-spike@1"})
	if err != nil {
		t.Fatal(err)
	}
	log, err := w.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if log.Genesis != corpusGenesis || log.Head != corpusHead || log.Depth != 14 {
		t.Fatal("wrong verified fixture frontier")
	}
	return repo, log
}

// Corpus C compares exactly the four named relations. The only exclusions
// are runtime/source identity and program names; neither changes these rows.
// Ordinary tests use the sealed oracle rows. The release gate ALSO runs the
// immutable old executable over the same bundle on both repetitions.
func TestCorpusC(t *testing.T) {
	repo, log := sealedCorpus(t)
	app, err := loadSource(os.DirFS("testdata/inventory"), "inventory-corpus")
	if err != nil {
		t.Fatal(err)
	}
	p := openLedger(t, app, log)
	if err := p.advanceFixture(context.Background(), log); err != nil {
		t.Fatal(err)
	}
	if !p.frontier.Complete || p.frontier.InterpretedPosition != 14 {
		t.Fatal("incomplete replay")
	}
	for _, query := range corpusQueries {
		t.Run(query.Name, func(t *testing.T) {
			want, err := os.ReadFile("testdata/inventory-" + query.Name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.queryFixture(context.Background(), query.SQL)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got.Rows)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(encoded, bytes.TrimSpace(want)) {
				t.Fatalf("candidate differs from sealed oracle:\ngot %s\nwant %s", encoded, want)
			}
			if oracle := os.Getenv("INVENTORY_CORPUS_ORACLE"); oracle != "" {
				output, err := exec.Command(oracle, "-repo", repo, "-database", filepath.Join(t.TempDir(), "old.sqlite"), "-sql", query.SQL).CombinedOutput()
				if err != nil {
					t.Fatalf("old oracle: %v %s", err, output)
				}
				var result struct {
					Rows json.RawMessage `json:"rows"`
				}
				if err := json.Unmarshal(output, &result); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(result.Rows, bytes.TrimSpace(want)) {
					t.Fatalf("live oracle differs from sealed rows: %s", output)
				}
				t.Log("live old oracle and candidate match sealed rows")
			}
		})
	}
}

func TestCorpusInputFreeze(t *testing.T) {
	_, log := sealedCorpus(t)
	// Empty causals, ordered causals and exact-bound payload all come from the
	// signed fixture. Full-input bytes cover every scalar, meta and rows.
	duplicate := log.Records[2]
	duplicate.Timestamp = -7
	duplicate.RestsOn = []string{log.Records[1].ID, log.Records[1].ID}
	for _, test := range []struct {
		name     string
		record   host.Record
		position int
	}{
		{"empty", log.Records[2], 3}, {"ordered", log.Records[4], 5},
		{"bound", log.Records[11], 12}, {"first-duplicate", duplicate, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, err := recordInput(test.record, test.position)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("testdata/input-" + test.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, bytes.TrimSpace(want)) {
				t.Fatalf("input changed:\n%s\nwant %s", got, want)
			}
		})
	}
}
