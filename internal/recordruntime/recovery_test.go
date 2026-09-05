package recordruntime

// Adapted from Gitseq3f4c4969 spike/jsonataddl/recovery_test.go. Completed
// mutations persist in order; each cold image is a prefix of those mutations
// plus an optional half next write. This is a process-death model, not an
// assertion about lost or reordered unsynced writes after a power failure.
import (
	"context"
	"database/sql"
	"fmt"
	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/vfs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

type mutationOp struct {
	file   string // "db", "wal", or "other"
	kind   string // "write", "truncate", "delete"
	offset int64
	size   int64
	data   []byte
}

type crashMarker struct {
	label string
	ops   int // mutation ops recorded before this marker
}

type crashRecorder struct {
	mu      sync.Mutex
	ops     []mutationOp
	markers []crashMarker
}

func (r *crashRecorder) record(op mutationOp) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

func (r *crashRecorder) marker(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markers = append(r.markers, crashMarker{label: label, ops: len(r.ops)})
}

func (r *crashRecorder) snapshot() ([]mutationOp, []crashMarker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mutationOp(nil), r.ops...), append([]crashMarker(nil), r.markers...)
}

type crashVFS struct {
	os       vfs.VFSFilename
	recorder *crashRecorder
}

func (v crashVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return v.os.Open(name, flags)
}

func (v crashVFS) OpenFilename(name *vfs.Filename, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	file, out, err := v.os.OpenFilename(name, flags)
	if err != nil {
		return file, out, err
	}
	if flags&(vfs.OPEN_DELETEONCLOSE|vfs.OPEN_TEMP_DB|vfs.OPEN_TRANSIENT_DB|vfs.OPEN_TEMP_JOURNAL|vfs.OPEN_SUBJOURNAL) != 0 {
		// Ephemeral files (statement subjournals, temp databases) are gone
		// after any crash and recovery never reads them; they are not part of
		// the durable state this recorder replays. A main or super journal
		// would matter, but WAL mode must never open one: leave those
		// classified "other" so the analysis fails loudly if one appears.
		return file, out, nil
	}
	kind := "other"
	switch {
	case flags&vfs.OPEN_MAIN_DB != 0:
		kind = "db"
	case flags&vfs.OPEN_WAL != 0:
		kind = "wal"
	case flags&vfs.OPEN_MAIN_JOURNAL != 0:
		// SQLite initializes a brand-new database in rollback-journal mode
		// before the journal_mode=wal switch takes hold, so the creation
		// window really does write a main journal and raw database pages.
		kind = "journal"
	}
	wrapped := &crashFile{File: file, kind: kind, recorder: v.recorder}
	if _, ok := file.(vfs.FileSharedMemory); ok {
		return crashFileShm{wrapped}, out, nil
	}
	return wrapped, out, nil
}

func (v crashVFS) Delete(name string, syncDir bool) error {
	if err := v.os.Delete(name, syncDir); err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(name, "-wal"):
		v.recorder.record(mutationOp{file: "wal", kind: "delete"})
	case strings.HasSuffix(name, "-journal"):
		v.recorder.record(mutationOp{file: "journal", kind: "delete"})
	case strings.HasSuffix(name, "-shm"):
		// The WAL index is volatile shared memory; its deletion is not a
		// durable mutation and cold recovery never reads it.
	default:
		v.recorder.record(mutationOp{file: "other", kind: "delete"})
	}
	return nil
}

func (v crashVFS) Access(name string, flags vfs.AccessFlag) (bool, error) {
	return v.os.Access(name, flags)
}

func (v crashVFS) FullPathname(name string) (string, error) {
	return v.os.FullPathname(name)
}

type crashFile struct {
	vfs.File
	kind     string
	recorder *crashRecorder
}

func (f *crashFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.File.WriteAt(p, off)
	if n > 0 {
		f.recorder.record(mutationOp{file: f.kind, kind: "write", offset: off, size: int64(n), data: append([]byte(nil), p[:n]...)})
	}
	return n, err
}

func (f *crashFile) Truncate(size int64) error {
	if err := f.File.Truncate(size); err != nil {
		return err
	}
	f.recorder.record(mutationOp{file: f.kind, kind: "truncate", size: size})
	return nil
}

func (f *crashFile) Unwrap() vfs.File { return f.File }

func (f *crashFile) DeviceCharacteristics() vfs.DeviceCharacteristic {
	// Batch-atomic writes would let SQLite skip durable steps this recorder
	// does not model; withhold the capability from the wrapped connection.
	return f.File.DeviceCharacteristics() &^ vfs.IOCAP_BATCH_ATOMIC
}

type crashFileShm struct {
	*crashFile
}

func (f crashFileShm) SharedMemory() vfs.SharedMemory {
	return f.crashFile.File.(vfs.FileSharedMemory).SharedMemory()
}

// materializeImage replays the first upto mutations (plus, when torn, half of
// the next write) onto empty files, reproducing the on-disk bytes at one
// crash instant.
func materializeImage(t *testing.T, dir string, ops []mutationOp, upto int, torn bool) string {
	t.Helper()
	paths := map[string]string{
		"db":      filepath.Join(dir, "crash.sqlite"),
		"wal":     filepath.Join(dir, "crash.sqlite-wal"),
		"journal": filepath.Join(dir, "crash.sqlite-journal"),
	}
	handles := map[string]*os.File{}
	handle := func(file string) *os.File {
		if open, exists := handles[file]; exists {
			return open
		}
		open, err := os.OpenFile(paths[file], os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		handles[file] = open
		return open
	}
	apply := func(op mutationOp, partial bool) {
		switch op.kind {
		case "write":
			data := op.data
			if partial {
				data = data[:len(data)/2]
			}
			if _, err := handle(op.file).WriteAt(data, op.offset); err != nil {
				t.Fatal(err)
			}
		case "truncate":
			if err := handle(op.file).Truncate(op.size); err != nil {
				t.Fatal(err)
			}
		case "delete":
			if open, exists := handles[op.file]; exists {
				open.Close()
				delete(handles, op.file)
			}
			if err := os.Remove(paths[op.file]); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	for index := 0; index < upto; index++ {
		apply(ops[index], false)
	}
	if torn {
		apply(ops[upto], true)
	}
	for _, open := range handles {
		if err := open.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return paths["db"]
}

type recoveredImage struct {
	present                               bool
	position, verifiedDepth               int
	interpretedEvent, gapEvent, gapReason string
}

func recoveryDSN(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }

func inspectRecoveryImage(t *testing.T, ctx context.Context, path string) (recoveredImage, string, error) {
	t.Helper()
	db, err := sqlitedriver.Open(recoveryDSN(path))
	if err != nil {
		return recoveredImage{}, "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return recoveredImage{}, "", err
	}
	if integrity != "ok" {
		return recoveredImage{}, "", fmt.Errorf("integrity: %s", integrity)
	}
	f := recoveredImage{}
	err = db.QueryRowContext(ctx, "SELECT interpreted_position,interpreted_event,verified_depth,gap_event,gap_reason FROM gitseq_frontier WHERE singleton=1").Scan(&f.position, &f.interpretedEvent, &f.verifiedDepth, &f.gapEvent, &f.gapReason)
	if err == nil {
		f.present = true
	} else if !strings.Contains(err.Error(), "no such table") {
		return f, "", err
	}
	return f, recoveryDump(t, ctx, db), nil
}

func recoveryDump(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT name,type FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND type IN ('table','view') ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	var tables []string
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&dump, "%s %s\n", kind, name)
		if kind == "table" && name != "gitseq_frontier" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+quote(table))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var values []string
		for rows.Next() {
			v, err := scanRow(rows, len(columns))
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, fmt.Sprintf("%#v", v))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(values)
		fmt.Fprintf(&dump, "rows %s\n", table)
		for _, v := range values {
			fmt.Fprintln(&dump, v)
		}
	}
	return dump.String()
}

func recoveryPrefix(log host.Log, n int) host.Log {
	log.Records = log.Records[:n]
	log.Depth = n
	log.Head = log.Genesis
	if n > 0 {
		_, log.Head, _ = strings.Cut(log.Records[n-1].ID, "#git:sha1:")
	}
	return log
}

func recoveryReferences(t *testing.T, ctx context.Context, app *jsonataddl.Application, log host.Log) []string {
	t.Helper()
	refs := make([]string, len(log.Records)+1)
	for n := range refs {
		path := filepath.Join(t.TempDir(), "reference.sqlite")
		prefix := recoveryPrefix(log, n)
		p, err := openFixture(ctx, path, app, prefix)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.advanceFixture(ctx, prefix); err != nil {
			p.close()
			t.Fatal(err)
		}
		if err = p.close(); err != nil {
			t.Fatal(err)
		}
		_, refs[n], err = inspectRecoveryImage(t, ctx, path)
		if err != nil {
			t.Fatal(err)
		}
	}
	return refs
}

func lastRecoveryWrite(ops []mutationOp, boundary int) int {
	for n := boundary - 1; n >= 0; n-- {
		if ops[n].file == "wal" && ops[n].kind == "write" {
			return n
		}
	}
	return -1
}

func TestRecoverySweep(t *testing.T) {
	ctx := context.Background()
	_, log := sealedCorpus(t)
	sources := fstest.MapFS{}
	for _, name := range []string{"application.sql", "folds/normalize.jsonata", "folds/inventory.jsonata"} {
		data, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = &fstest.MapFile{Data: data}
	}
	app, err := LoadSource(sources, "gitseq-inventory")
	if err != nil {
		t.Fatal(err)
	}
	refs := recoveryReferences(t, ctx, app, log)
	gapPosition := 0
	for i, record := range log.Records {
		if record.Schema == "stock_received" {
			gapPosition = i + 1
			break
		}
	}
	if gapPosition == 0 {
		t.Fatal("fixture has no stock write to roll back")
	}
	for _, gap := range []bool{false, true} {
		t.Run(fmt.Sprintf("gap=%v", gap), func(t *testing.T) {
			recorder := &crashRecorder{}
			name := fmt.Sprintf("inventory-recovery-%v", gap)
			vfs.Register(name, crashVFS{os: vfs.Find("os").(vfs.VFSFilename), recorder: recorder})
			defer vfs.Unregister(name)
			p, err := openFixtureVFS(ctx, filepath.Join(t.TempDir(), "writer.sqlite"), app, log, name)
			if err != nil {
				t.Fatal(err)
			}
			recorder.marker("init")
			committed := 0
			p.afterStep = func(position int) {
				committed = position
				recorder.marker(fmt.Sprintf("event-%d", position))
				if !gap && position == 5 {
					var busy, frames, done int
					if err := p.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &done); err != nil || busy != 0 {
						t.Fatalf("checkpoint: %v busy=%d", err, busy)
					}
					recorder.marker("checkpoint")
				}
			}
			if gap {
				p.checkpoint = func(stage string) error {
					if stage == "after-changes" && committed == gapPosition-1 {
						return fmt.Errorf("injected failure after tentative changes")
					}
					return nil
				}
			}
			err = p.advanceFixture(ctx, log)
			if gap {
				if err == nil || !strings.Contains(err.Error(), "injected failure") || p.frontier.InterpretedPosition != gapPosition-1 {
					t.Fatalf("gap: %+v %v", p.frontier, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			recorder.marker("finished")
			if err = p.close(); err != nil {
				t.Fatal(err)
			}
			ops, markers := recorder.snapshot()
			initCommit, finishCommit := -1, -1
			eventCommits := map[int]int{}
			previous := 0
			multiFrame, backfill, resets := false, 0, 0
			for _, m := range markers {
				last := lastRecoveryWrite(ops, m.ops)
				switch m.label {
				case "init":
					initCommit = last
				case "finished":
					finishCommit = last
				default:
					var position int
					if _, err := fmt.Sscanf(m.label, "event-%d", &position); err == nil {
						eventCommits[position] = last
						count := 0
						for _, op := range ops[previous:m.ops] {
							if op.file == "wal" && op.kind == "write" {
								count++
							}
						}
						multiFrame = multiFrame || count > 1
					}
				}
				previous = m.ops
			}
			for _, op := range ops {
				if op.file == "other" {
					t.Fatalf("unmodeled operation: %+v", op)
				}
				if op.file == "db" && op.kind == "write" {
					backfill++
				}
				if op.file == "wal" && (op.kind == "truncate" || op.kind == "delete") {
					resets++
				}
			}
			if initCommit < 0 || len(eventCommits) != committed || !multiFrame || backfill == 0 || resets == 0 {
				t.Fatalf("missing interruption coverage: init=%d commits=%d/%d multi=%v backfill=%d resets=%d", initCommit, len(eventCommits), committed, multiFrame, backfill, resets)
			}
			verified, discarded := 0, 0
			images := t.TempDir()
			verify := func(boundary int, torn bool) {
				dir := filepath.Join(images, fmt.Sprintf("%d-%v", boundary, torn))
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir)
				path := materializeImage(t, dir, ops, boundary, torn)
				f, dump, err := inspectRecoveryImage(t, ctx, path)
				if err != nil {
					if boundary > initCommit {
						t.Fatalf("op%d torn=%v unreadable after initialization: %v", boundary, torn, err)
					}
					discarded++
					return
				}
				if !f.present {
					if boundary > initCommit || dump != "" {
						t.Fatalf("op%d torn=%v partial initialized schema: %s", boundary, torn, dump)
					}
					verified++
					return
				}
				expected := 0
				for _, commit := range eventCommits {
					if commit < boundary {
						expected++
					}
				}
				if f.position != expected || f.verifiedDepth != log.Depth {
					t.Fatalf("op%d torn=%v frontier=%+v expected position%d/depth%d", boundary, torn, f, expected, log.Depth)
				}
				wantEvent := ""
				if expected > 0 {
					wantEvent = log.Records[expected-1].ID
				}
				if f.interpretedEvent != wantEvent {
					t.Fatalf("op%d frontier event differs", boundary)
				}
				wantGap := ""
				if gap && finishCommit < boundary {
					wantGap = log.Records[gapPosition-1].ID
				}
				if f.gapEvent != wantGap || (f.gapEvent == "") != (f.gapReason == "") {
					t.Fatalf("op%d gap metadata=%+v want %q", boundary, f, wantGap)
				}
				if dump != refs[f.position] {
					t.Fatalf("op%d torn=%v state differs from clean prefix%d\nrecovered:\n%s\nexpected:\n%s", boundary, torn, f.position, dump, refs[f.position])
				}
				verified++
			}
			for boundary := 0; boundary <= len(ops); boundary++ {
				verify(boundary, false)
				if boundary < len(ops) && ops[boundary].kind == "write" {
					verify(boundary, true)
				}
			}
			t.Logf("verified %d images; discarded %d unreadable initialization images; mutations=%d committed=%d init=%d finish=%d backfill=%d resets=%d", verified, discarded, len(ops), committed, initCommit, finishCommit, backfill, resets)
		})
	}
}
