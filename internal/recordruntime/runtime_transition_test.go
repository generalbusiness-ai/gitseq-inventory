package recordruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

const previousInputRuntime = "jsonata-ddl-runtime:sha256:d506811d6e568fc3e4c0f9773d1d0e12949cf6ab3f6bc891d8a1db7bf0aa90cd"

type runtimeFiles struct {
	durable    map[string][]byte
	shmPresent bool
}

func snapshotRuntimeFiles(t *testing.T, path string) runtimeFiles {
	t.Helper()
	snapshot := runtimeFiles{durable: map[string][]byte{}}
	for _, suffix := range []string{"", "-wal", "-journal"} {
		data, err := os.ReadFile(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		snapshot.durable[suffix] = data
	}
	_, err := os.Stat(path + "-shm")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	snapshot.shmPresent = err == nil
	// SHM bytes contain volatile read-lock bookkeeping, not durable state.
	return snapshot
}

func TestPreviousRuntimeReopenRefusesUnchanged(t *testing.T) {
	for _, mode := range []string{"closed", "read-only", "live-wal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			app := ledger(t)
			log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
			path := filepath.Join(t.TempDir(), "old.sqlite")
			p, err := openFixture(ctx, path, app, log)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			if err := p.advanceFixture(ctx, log); err != nil {
				t.Fatal(err)
			}
			if _, err := p.db.Exec("PRAGMA wal_autocheckpoint=0; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				t.Fatal(err)
			}
			mainBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Only persisted runtime changes: both handles and all table shapes are
			// current. In the WAL case the main file still holds the current runtime.
			if _, err := p.db.Exec("UPDATE gitseq_projection_identity SET runtime=?", previousInputRuntime); err != nil {
				t.Fatal(err)
			}
			if mode == "live-wal" {
				mainAfter, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(mainBefore, mainAfter) {
					t.Fatal("runtime edit was not confined to WAL")
				}
				wal, err := os.Stat(path + "-wal")
				if err != nil || wal.Size() == 0 {
					t.Fatal("no live WAL control")
				}
			} else {
				if err := p.close(); err != nil {
					t.Fatal(err)
				}
				if mode == "read-only" {
					if err := os.Chmod(path, 0400); err != nil {
						t.Fatal(err)
					}
					defer os.Chmod(path, 0600)
				}
			}
			before := snapshotRuntimeFiles(t, path)
			opened, err := openFixture(ctx, path, app, log)
			if err == nil {
				opened.close()
				t.Fatal("old persisted runtime was reset or reused")
			}
			if !strings.Contains(err.Error(), "stored runtime differs") {
				t.Fatalf("refusal did not inspect actual stored runtime: %v", err)
			}
			assertRuntimeFilesUnchanged(t, path, before)
		})
	}
}

func TestPreviousRuntimeContinueRefusesUnchanged(t *testing.T) {
	ctx := context.Background()
	app := ledger(t)
	log := unitLog(`{"id":"a","qty":2,"sku":"ink"}`)
	p := openLedger(t, app, log)
	if err := p.advanceFixture(ctx, log); err != nil {
		t.Fatal(err)
	}
	files := ledgerSources()
	files["application.sql"].Data = []byte(strings.Replace(string(files["application.sql"].Data), "WRITES ledger;", "WRITES ledger, extra;", 1) + "\nCREATE TABLE extra (id TEXT NOT NULL, PRIMARY KEY(id));")
	next, err := LoadSource(files, "adapter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Exec("UPDATE gitseq_projection_identity SET runtime=?", previousInputRuntime); err != nil {
		t.Fatal(err)
	}
	beforeDump := recoveryDump(t, ctx, p.db)
	beforeFrontier, beforeApp := p.frontier, p.app
	before := snapshotRuntimeFiles(t, p.path)
	if err := p.continueFixture(ctx, next); err == nil || !strings.Contains(err.Error(), "stored runtime differs") {
		t.Fatalf("old persisted runtime was relabelled under current handles: %v", err)
	}
	assertRuntimeFilesUnchanged(t, p.path, before)
	if afterDump := recoveryDump(t, ctx, p.db); afterDump != beforeDump {
		t.Fatal("continuation refusal changed stored rows/schema/identity")
	}
	if err := p.loadFrontier(ctx); err != nil {
		t.Fatal(err)
	}
	if p.frontier != beforeFrontier || p.app != beforeApp {
		t.Fatal("continuation refusal changed frontier or compiled handle")
	}
}

// SQLite may create volatile SHM and a zero-byte WAL for a read-only probe.
// Existing durable files must remain byte-identical; new transaction frames,
// journal files, or removal of an existing sidecar are never permitted here.
func assertRuntimeFilesUnchanged(t *testing.T, path string, before runtimeFiles) {
	t.Helper()
	after := snapshotRuntimeFiles(t, path)
	for suffix, data := range before.durable {
		got, present := after.durable[suffix]
		if !present || !bytes.Equal(data, got) {
			t.Fatalf("refusal changed existing durable file %q", suffix)
		}
	}
	for suffix, data := range after.durable {
		if _, present := before.durable[suffix]; !present {
			if suffix != "-wal" || len(data) != 0 {
				t.Fatalf("refusal created durable file/data %q (%d bytes)", suffix, len(data))
			}
			t.Log("read-only coordination created an empty WAL with zero transaction frames")
		}
	}
	if before.shmPresent && !after.shmPresent {
		t.Fatal("read-only probe removed existing SHM")
	}
	if !before.shmPresent && after.shmPresent {
		t.Log("read-only coordination created volatile SHM")
	}
}

func TestForeignHandleRefusesStorage(t *testing.T) {
	current, err := Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"runtime", "dialect"} {
		t.Run(variant, func(t *testing.T) {
			dialect, runtime := GitseqRecord(), current.Digest()
			if variant == "runtime" {
				runtime = previousInputRuntime
			} else {
				dialect.Limits.MaxInputDepth++
			}
			app, err := jsonataddl.LoadApplication(ledgerSources(), ".", "adapter-fixture", dialect, runtime)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
			opened, err := openFixture(context.Background(), path, app, unitLog())
			if err == nil {
				opened.close()
				t.Fatal("foreign handle created storage")
			}
			if !strings.Contains(err.Error(), "handle does not match") {
				t.Fatalf("identity admission guard not reached: %v", err)
			}
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatalf("identity refusal created %q", suffix)
				}
			}
		})
	}
}
