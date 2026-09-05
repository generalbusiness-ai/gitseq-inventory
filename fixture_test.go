package inventory

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq-inventory/internal/recordruntime"
	"github.com/generalbusiness-ai/gitseq/host"
)

func TestPublicFixtureBoundary(t *testing.T) {
	ctx := context.Background()
	binding, err := Binding()
	if err != nil {
		t.Fatal(err)
	}
	for _, variation := range []string{"example", "payload", "schema", "extra", "short", "causal", "actor", "old-binding"} {
		t.Run(variation, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "events")
			if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			_, signer, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			selectedBinding := binding
			if variation == "old-binding" {
				selectedBinding.FoldVersion = "jsonata-v206-sqlite-spike@0"
			}
			ws, err := host.Init(ctx, repo, selectedBinding, signer, host.Options{})
			if err != nil {
				t.Fatal(err)
			}
			acts := []host.Act{
				{Schema: "stock_received", Payload: []byte(`{"id":"stock-1","qty":5,"sku":"ink"}`)},
				{Schema: "reservation_requested", Payload: []byte(`{"id":"reservation-1","qty":2,"sku":"ink"}`)},
				{Schema: "reservation_requested", Payload: []byte(`{"id":"reservation-2","qty":4,"sku":"ink"}`)},
			}
			if variation == "payload" {
				acts[0].Payload = []byte(`{"id":"stock-1","qty":6,"sku":"ink"}`)
			}
			if variation == "schema" {
				acts[0].Schema = "Stock_received"
			}
			if variation == "extra" {
				acts = append(acts, host.Act{Schema: "unrelated", Payload: []byte(`{}`)})
			}
			if variation == "short" {
				acts = acts[:2]
			}
			initial, err := ws.Records(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if variation == "causal" {
				acts[0].RestsOn = []string{initial.Records[0].ID}
			}
			if variation == "actor" {
				_, signer, err = ed25519.GenerateKey(nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, act := range acts {
				if _, err := ws.Append(ctx, signer, act); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "projection.sqlite")
			if variation != "example" {
				for _, existing := range []bool{false, true} {
					sentinel := []byte("unrelated database bytes")
					if existing {
						if err := os.WriteFile(path, sentinel, 0600); err != nil {
							t.Fatal(err)
						}
					}
					p, err := OpenFixture(ctx, repo, path)
					if err == nil {
						p.Close()
						t.Fatal("non-fixture accepted")
					}
					if variation != "old-binding" && !strings.Contains(err.Error(), "fixture-only runtime") {
						t.Fatalf("refusal happened after admission: %v", err)
					}
					got, readErr := os.ReadFile(path)
					if existing {
						if readErr != nil || !bytes.Equal(got, sentinel) {
							t.Fatal("rejection changed existing bytes")
						}
					} else if !os.IsNotExist(readErr) {
						t.Fatal("rejection created database")
					}
					for _, suffix := range []string{"-wal", "-shm"} {
						if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
							t.Fatal("rejection created sidecar")
						}
					}
				}
				return
			}
			// Both rebuild and cache reuse must preserve one decision per recognized act.
			for range 2 {
				p, err := OpenFixture(ctx, repo, path)
				if err != nil {
					t.Fatal(err)
				}
				result, err := p.Query(ctx, "SELECT count(*) FROM gitseq_decisions")
				if err != nil || !reflect.DeepEqual(result.Rows, [][]any{{int64(3)}}) || !result.Frontier.Complete {
					t.Fatalf("replay/reuse: %#v %v", result, err)
				}
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestBindingAndProjectionIdentity(t *testing.T) {
	ctx := context.Background()
	app, log := exampleLog(t, ctx)
	binding, err := Binding()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := recordruntime.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if binding.FoldVersion != identity.Digest() || app.RuntimeProfile() != identity.Digest() {
		t.Fatal("binding does not select composed runtime")
	}
	p := buildProjection(t, ctx, app, log, filepath.Join(t.TempDir(), "identity.sqlite"))
	defer p.Close()
	result, err := p.Query(ctx, "SELECT application,runtime,revision,storage_schema,export_contract FROM gitseq_projection_identity")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{binding.Name, identity.Digest(), app.Revision(), app.StorageSchemaDigest(), app.ExportContractDigest()}}
	if !reflect.DeepEqual(result.Rows, want) {
		a, _ := json.Marshal(result.Rows)
		t.Fatalf("projection identity: %s", a)
	}
}
