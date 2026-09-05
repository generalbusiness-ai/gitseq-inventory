// inventory-fixture creates a sealed example event repository for the README
// path. It is demonstration data, not a production event-submission service.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/generalbusiness-ai/gitseq-inventory"
	"github.com/generalbusiness-ai/gitseq/host"
)

type exampleAct struct {
	schema  string
	payload map[string]any
}

var exampleActs = []exampleAct{
	{schema: "stock_received", payload: map[string]any{"id": "stock-1", "sku": "ink", "qty": 5}},
	{schema: "reservation_requested", payload: map[string]any{"id": "reservation-1", "sku": "ink", "qty": 2}},
	{schema: "reservation_requested", payload: map[string]any{"id": "reservation-2", "sku": "ink", "qty": 4}},
}

func main() {
	var repo string
	flag.StringVar(&repo, "repo", "", "new path for the example Git repository")
	flag.Parse()
	if repo == "" {
		fmt.Fprintln(os.Stderr, "usage: inventory-fixture -repo NEW_PATH")
		os.Exit(2)
	}
	if err := createFixture(context.Background(), repo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func createFixture(ctx context.Context, repo string) error {
	if _, err := os.Stat(repo); err == nil {
		return fmt.Errorf("fixture repository already exists: %s", repo)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		return err
	}
	if output, err := exec.CommandContext(ctx, "git", "init", "-q", repo).CombinedOutput(); err != nil {
		return fmt.Errorf("initialize Git repository: %w: %s", err, output)
	}
	_, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	binding, err := inventory.Binding()
	if err != nil {
		return err
	}
	workspace, err := host.Init(ctx, repo, binding, signer, host.Options{})
	if err != nil {
		return err
	}
	for index, act := range exampleActs {
		payload, err := json.Marshal(act.payload)
		if err != nil {
			return err
		}
		if _, err := workspace.Append(ctx, signer, host.Act{
			Schema:         act.schema,
			Payload:        payload,
			IdempotencyKey: fmt.Sprintf("example-%d", index+1),
		}); err != nil {
			return err
		}
	}
	reopened, err := host.Open(ctx, repo, binding)
	if err != nil {
		return err
	}
	log, err := reopened.Records(ctx)
	if err != nil {
		return err
	}
	sequencerKey := filepath.Join(repo, ".git", "gitseq", "sequencer")
	if err := os.Remove(sequencerKey); err != nil {
		return fmt.Errorf("seal fixture by removing sequencer key: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"repo": repo, "genesis": log.Genesis, "head": log.Head, "depth": log.Depth,
	})
}
