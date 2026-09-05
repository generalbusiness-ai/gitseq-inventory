package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	inventory "github.com/generalbusiness-ai/gitseq-inventory"
	"github.com/generalbusiness-ai/gitseq/host"
)

func TestCreateFixtureSealsExampleLog(t *testing.T) {
	ctx := context.Background()
	repository := filepath.Join(t.TempDir(), "events")
	if err := createFixture(ctx, repository); err != nil {
		t.Fatal(err)
	}

	sequencerKey := filepath.Join(repository, ".git", "gitseq", "sequencer")
	if _, err := os.Stat(sequencerKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sequencer private key remains after sealing: %v", err)
	}

	binding, err := inventory.Binding()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := host.Open(ctx, repository, binding)
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"id": "late", "sku": "ink", "qty": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Append(ctx, signer, host.Act{
		Schema: "stock_received", Payload: payload, IdempotencyKey: "late-append",
	}); err == nil {
		t.Fatal("sealed fixture accepted another event")
	}
	after, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("failed append moved sealed log from %s/%d to %s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}
