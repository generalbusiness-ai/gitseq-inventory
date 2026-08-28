package inventory

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/spike/jsonataddl"
)

func TestReviewedApplicationSourcesAreUnchanged(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"application.sql", "5b11080762dbbf4a0ee056923158c46f04dff15e4b31b68551dcdf8b251f712f"},
		{"folds/inventory.jsonata", "6d9b86fcf2f2969de5f45f622c0bbc1886533bd004826e8f95a56091dd6ecdd8"},
	}
	for _, test := range tests {
		source, err := sourceFiles.ReadFile(test.path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(source)
		if got := hex.EncodeToString(digest[:]); got != test.want {
			t.Errorf("%s digest = %s, want reviewed source %s", test.path, got, test.want)
		}
	}
}

func TestInventoryReplaysIntoEquivalentBoundedProjections(t *testing.T) {
	ctx := context.Background()
	profile, log := exampleLog(t, ctx)
	first := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "first.sqlite"))
	defer first.Close()
	second := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "second.sqlite"))
	defer second.Close()

	query := `SELECT d.position, d.event_type, d.decision, f.kind,
             (SELECT available FROM stock WHERE sku = 'ink') AS available
             FROM gitseq_decisions d LEFT JOIN gitseq_facts f USING (event_id)
             ORDER BY d.position`
	firstResult, err := first.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := second.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstResult, secondResult) {
		t.Fatalf("replays differ:\nfirst: %#v\nsecond: %#v", firstResult, secondResult)
	}
	if !firstResult.Frontier.Complete || firstResult.Frontier.VerifiedHead != log.Head || firstResult.Frontier.VerifiedDepth != log.Depth {
		t.Fatalf("wrong exact frontier: %#v, log %#v", firstResult.Frontier, log)
	}
	want := [][]any{
		{int64(2), "stock_received", "effective", nil, int64(3)},
		{int64(3), "reservation_requested", "effective", nil, int64(3)},
		{int64(4), "reservation_requested", "ineffective", "insufficient-stock", int64(3)},
	}
	if !reflect.DeepEqual(firstResult.Rows, want) {
		t.Fatalf("rows = %#v, want %#v", firstResult.Rows, want)
	}
}

func TestApplicationQuerySurfaceRemainsReadOnly(t *testing.T) {
	ctx := context.Background()
	profile, log := exampleLog(t, ctx)
	projection := buildProjection(t, ctx, profile, log, filepath.Join(t.TempDir(), "readonly.sqlite"))
	defer projection.Close()
	for _, query := range []string{
		`UPDATE stock SET available = 0`,
		`PRAGMA table_info(stock)`,
		`SELECT random()`,
		`SELECT 1; SELECT 2`,
	} {
		if _, err := projection.Query(ctx, query); err == nil {
			t.Errorf("Query(%q) succeeded", query)
		}
	}
}

type testAct struct {
	schema  string
	payload map[string]any
}

func exampleLog(t *testing.T, ctx context.Context) (*jsonataddl.Profile, host.Log) {
	t.Helper()
	profile, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(t.TempDir(), "events")
	if output, err := exec.CommandContext(ctx, "git", "init", "-q", repository).CombinedOutput(); err != nil {
		t.Fatalf("initialize Git repository: %v: %s", err, output)
	}
	_, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := host.Init(ctx, repository, profile.Application, signer, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	acts := []testAct{
		{schema: "stock_received", payload: map[string]any{"id": "stock-1", "sku": "ink", "qty": 5}},
		{schema: "reservation_requested", payload: map[string]any{"id": "reservation-1", "sku": "ink", "qty": 2}},
		{schema: "reservation_requested", payload: map[string]any{"id": "reservation-2", "sku": "ink", "qty": 4}},
	}
	for index, act := range acts {
		payload, err := json.Marshal(act.payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Append(ctx, signer, host.Act{Schema: act.schema, Payload: payload, IdempotencyKey: act.schema + string(rune('a'+index))}); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := host.Open(ctx, repository, profile.Application)
	if err != nil {
		t.Fatal(err)
	}
	log, err := reopened.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return profile, log
}

func buildProjection(t *testing.T, ctx context.Context, profile *jsonataddl.Profile, log host.Log, path string) *jsonataddl.Projection {
	t.Helper()
	projection, err := jsonataddl.Build(ctx, profile, log, path)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
