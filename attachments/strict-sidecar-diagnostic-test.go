package recordruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
			if after := snapshotRuntimeFiles(t, path); !reflect.DeepEqual(before, after) {
				for _, suffix := range []string{"", "-wal", "-journal"} { t.Logf("%q before=%d after=%d equal=%v", suffix, len(before.durable[suffix]), len(after.durable[suffix]), bytes.Equal(before.durable[suffix], after.durable[suffix])) }
				t.Logf("SHM before=%v after=%v", before.shmPresent, after.shmPresent)
				t.Fatal("refusal changed database/WAL/journal bytes or SHM existence")
			}
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
	if after := snapshotRuntimeFiles(t, p.path); !reflect.DeepEqual(before, after) {
		t.Fatal("continuation refusal changed database/WAL/journal bytes or SHM existence")
	}
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
