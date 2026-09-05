package recordruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
	"github.com/ncruces/go-sqlite3"
	sqlitedriver "github.com/ncruces/go-sqlite3/driver"
)

const projectionID = 0x47534946 // GSIF: disposable Gitseq inventory fixture.

type frontier struct {
	Genesis             string `json:"genesis"`
	VerifiedHead        string `json:"verified_head"`
	VerifiedDepth       int    `json:"verified_depth"`
	InterpretedEvent    string `json:"interpreted_event"`
	InterpretedPosition int    `json:"interpreted_position"`
	GapEvent            string `json:"gap_event,omitempty"`
	GapReason           string `json:"gap_reason,omitempty"`
	Complete            bool   `json:"complete"`
}

// The mutable runtime stays private. Public fixture callers cannot advance or
// continue it; they receive only a completed read-only demonstration handle.
type projection struct {
	mu         sync.RWMutex
	path       string
	db, reader *sql.DB
	app        *jsonataddl.Application
	guard      *readGuard
	frontier   frontier
	// Tests inject storage failures at actual transaction boundaries.
	checkpoint func(string) error
}

func validateHandle(app *jsonataddl.Application) error {
	identity, err := Identity()
	if err != nil {
		return err
	}
	if app == nil || app.RuntimeProfile() != identity.Digest() || app.Dialect().Canonical() != GitseqRecord().Canonical() {
		return errors.New("handle does not match the inventory runtime identity")
	}
	normalizer := app.NormalizerProgram()
	if len(normalizer.Reads) != 0 || len(normalizer.Writes) != 0 || len(app.Folds()) != 1 {
		return errors.New("inventory requires a read-free, write-free normalizer and one analytic fold")
	}
	for name := range app.Tables() {
		if strings.HasPrefix(strings.ToLower(name), "gitseq_") || strings.HasPrefix(name, "__") {
			return errors.New("application table uses a host-reserved name")
		}
	}
	return nil
}

func openFixture(ctx context.Context, path string, app *jsonataddl.Application, log host.Log) (*projection, error) {
	if err := validateHandle(app); err != nil {
		return nil, err
	}
	if log.Genesis == "" || log.Head == "" || log.Depth != len(log.Records) {
		return nil, errors.New("verified log frontier is incomplete")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	fresh := false
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		fresh = true
		err = file.Close()
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("projection path must be a regular file")
	}
	// Inspect existing files through a read-only connection before enabling
	// WAL or making any other writer change.
	if !fresh {
		u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
		probe, err := sqlitedriver.Open(u.String())
		if err != nil {
			return nil, err
		}
		var id int
		err = probe.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id)
		if err == nil && id == projectionID {
			err = refuseLegacyJSON(ctx, probe)
		}
		err = errors.Join(err, probe.Close())
		if err != nil {
			return nil, err
		}
		if id != projectionID {
			return nil, errors.New("existing file is not an inventory fixture projection")
		}
	}
	p := &projection{app: app, guard: &readGuard{}, path: path}
	p.db, err = openDB(ctx, path, p.guard, false, app)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*projection, error) { return nil, errors.Join(err, p.db.Close()) }
	if fresh {
		if _, err = p.db.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d", projectionID)); err != nil {
			return fail(err)
		}
	} else {
		var id int
		if err := p.db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
			return fail(err)
		}
		if id != projectionID {
			return fail(errors.New("existing file is not an inventory fixture projection"))
		}
	}
	reuse := false
	if !fresh {
		var genesis, name, runtime, revision, schema string
		err = p.db.QueryRowContext(ctx, "SELECT genesis, application, runtime, revision, storage_schema FROM gitseq_projection_identity WHERE singleton=1").Scan(&genesis, &name, &runtime, &revision, &schema)
		if err != nil {
			return fail(err)
		}
		reuse = genesis == log.Genesis && name == app.Name() && runtime == app.RuntimeProfile() && revision == app.Revision() && schema == app.StorageSchemaDigest()
		if reuse {
			if err := p.loadFrontier(ctx); err != nil {
				return fail(err)
			}
			pos := p.frontier.InterpretedPosition
			reuse = pos >= 0 && pos <= len(log.Records) && (pos == 0 || log.Records[pos-1].ID == p.frontier.InterpretedEvent)
		}
	}
	if !reuse {
		if err := p.reset(ctx, log); err != nil {
			return fail(err)
		}
	}
	p.reader, err = openDB(ctx, path, nil, true, app)
	if err != nil {
		return fail(err)
	}
	return p, nil
}

// ddl/1 declared JSON with SQLite NUMERIC affinity, which may already have
// coerced scalar bytes. Refuse it before any writer/reset/continue operation;
// recompiling source under ddl/2 cannot repair the stored representation.
func refuseLegacyJSON(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) error {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master AS m
JOIN pragma_table_info(m.name) AS p WHERE m.type='table' AND upper(trim(p.type))='JSON'`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("legacy JSON-affinity projection requires an explicit discard and verified replay; automatic reset and continuation are refused")
	}
	return nil
}

func openDB(ctx context.Context, path string, guard *readGuard, readOnly bool, app *jsonataddl.Application) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
		q.Add("_pragma", "journal_mode(WAL)")
	}
	u.RawQuery = q.Encode()
	db, err := sqlitedriver.Open(u.String(), func(conn *sqlite3.Conn) error {
		for _, config := range []struct {
			op    sqlite3.DBConfig
			value bool
		}{{sqlite3.DBCONFIG_DEFENSIVE, true}, {sqlite3.DBCONFIG_TRUSTED_SCHEMA, false}, {sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION, false}} {
			if _, err := conn.Config(config.op, config.value); err != nil {
				return err
			}
		}
		conn.Limit(sqlite3.LIMIT_LENGTH, 256<<10)
		conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 64<<10)
		conn.Limit(sqlite3.LIMIT_ATTACHED, 0)
		conn.Limit(sqlite3.LIMIT_EXPR_DEPTH, 32)
		if readOnly {
			return conn.SetAuthorizer(queryAuthorizer(app))
		}
		return conn.SetAuthorizer(guard.check)
	})
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func (p *projection) reset(ctx context.Context, log host.Log) (err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	rows, err := tx.QueryContext(ctx, "SELECT type,name FROM sqlite_master WHERE type IN ('view','table') AND name NOT LIKE 'sqlite_%' ORDER BY type DESC,name")
	if err != nil {
		return err
	}
	var drops []string
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			rows.Close()
			return err
		}
		drops = append(drops, "DROP "+strings.ToUpper(kind)+" "+quote(name))
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	statements := append(drops, hostSchema...)
	statements = append(statements, p.app.SchemaSQL()...)
	statements = append(statements, p.app.ReplaceableSQL()...)
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err = p.saveIdentity(ctx, tx, p.app, log.Genesis); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gitseq_frontier VALUES (1,?,?,?,0,'','','')", log.Genesis, log.Head, log.Depth); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	p.frontier = frontier{Genesis: log.Genesis, VerifiedHead: log.Head, VerifiedDepth: log.Depth}
	return nil
}

var hostSchema = []string{
	`CREATE TABLE gitseq_projection_identity (singleton INTEGER PRIMARY KEY CHECK(singleton=1), genesis TEXT NOT NULL, application TEXT NOT NULL, runtime TEXT NOT NULL, revision TEXT NOT NULL, storage_schema TEXT NOT NULL, export_contract TEXT NOT NULL) STRICT`,
	`CREATE TABLE gitseq_frontier (singleton INTEGER PRIMARY KEY CHECK(singleton=1), genesis TEXT NOT NULL, verified_head TEXT NOT NULL, verified_depth INTEGER NOT NULL, interpreted_position INTEGER NOT NULL, interpreted_event TEXT NOT NULL, gap_event TEXT NOT NULL, gap_reason TEXT NOT NULL) STRICT`,
	`CREATE TABLE gitseq_decisions (position INTEGER PRIMARY KEY, event_id TEXT NOT NULL UNIQUE, event_type TEXT NOT NULL, decision TEXT NOT NULL CHECK(decision IN ('effective','ineffective'))) STRICT`,
	`CREATE TABLE gitseq_facts (position INTEGER NOT NULL, event_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, fact_json TEXT NOT NULL, PRIMARY KEY(event_id,ordinal)) STRICT`,
	`CREATE TABLE gitseq_row_provenance (position INTEGER NOT NULL, event_id TEXT NOT NULL, ordinal INTEGER NOT NULL, table_name TEXT NOT NULL, key_json TEXT NOT NULL, operation TEXT NOT NULL, PRIMARY KEY(event_id,ordinal)) STRICT`,
	`CREATE TABLE __gitseq_row_versions (table_name TEXT NOT NULL, key_json TEXT NOT NULL, version INTEGER NOT NULL, event_id TEXT NOT NULL, PRIMARY KEY(table_name,key_json)) STRICT`,
}

func (p *projection) saveIdentity(ctx context.Context, tx *sql.Tx, app *jsonataddl.Application, genesis string) error {
	_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO gitseq_projection_identity VALUES (1,?,?,?,?,?,?)", genesis, app.Name(), app.RuntimeProfile(), app.Revision(), app.StorageSchemaDigest(), app.ExportContractDigest())
	return err
}

func (p *projection) loadFrontier(ctx context.Context) error {
	f := frontier{}
	err := p.db.QueryRowContext(ctx, "SELECT genesis,verified_head,verified_depth,interpreted_position,interpreted_event,gap_event,gap_reason FROM gitseq_frontier WHERE singleton=1").Scan(&f.Genesis, &f.VerifiedHead, &f.VerifiedDepth, &f.InterpretedPosition, &f.InterpretedEvent, &f.GapEvent, &f.GapReason)
	if err != nil {
		return err
	}
	f.Complete = f.InterpretedPosition == f.VerifiedDepth && f.GapEvent == ""
	p.frontier = f
	return nil
}

func (p *projection) step(ctx context.Context, record host.Record, position int) (err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if inventorySchema(record.Schema) {
		input, err := recordInput(record, position)
		if err != nil {
			return err
		}
		normalized, err := p.app.Evaluate(p.app.NormalizerProgram().Name, input)
		if err != nil {
			return err
		}
		emitted := normalized.Events[p.app.Event().Name]
		if normalized.Decision != "effective" || len(normalized.Tables) != 0 || len(normalized.Facts) != 0 || len(emitted) != 1 {
			return errors.New("recognized inventory record requires exactly one effective normalizer emission")
		}
		fold := p.app.Folds()[0]
		input, err = readInput(ctx, tx, p.app, p.guard, fold.Name, emitted[0])
		if err != nil {
			return err
		}
		result, e := p.app.Evaluate(fold.Name, input)
		if e != nil {
			return e
		}
		if err = p.apply(ctx, tx, result.Tables, record, position); err != nil {
			return err
		}
		if err = p.checkpointAt("after-changes"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO gitseq_decisions VALUES (?,?,?,?)", position, record.ID, record.Schema, result.Decision); err != nil {
			return err
		}
		for ordinal, fact := range result.Facts {
			kind, ok := fact["kind"].(string)
			if !ok || kind == "" {
				return errors.New("fold fact needs a non-empty kind")
			}
			encoded, e := json.Marshal(fact)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO gitseq_facts VALUES (?,?,?,?,?)", position, record.ID, ordinal, kind, string(encoded)); err != nil {
				return err
			}
		}
	}
	if err = p.checkpointAt("before-frontier"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE gitseq_frontier SET interpreted_position=?,interpreted_event=?,gap_event='',gap_reason='' WHERE singleton=1", position, record.ID); err != nil {
		return err
	}
	if err = p.checkpointAt("before-commit"); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *projection) checkpointAt(stage string) error {
	if p.checkpoint != nil {
		return p.checkpoint(stage)
	}
	return nil
}

func (p *projection) apply(ctx context.Context, tx *sql.Tx, changes map[string]jsonataddl.TableChanges, record host.Record, position int) error {
	tables := p.app.Tables()
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	sort.Strings(names)
	ordinal := 0
	for _, name := range names {
		table, ok := tables[name]
		if !ok {
			return errors.New("mutation names an undeclared table")
		}
		change := changes[name]
		for _, operation := range []struct {
			name string
			rows []map[string]any
		}{{"insert", change.Insert}, {"upsert", change.Upsert}, {"delete", change.Delete}} {
			for _, row := range operation.rows {
				if err := writeRow(ctx, tx, table, operation.name, row); err != nil {
					return err
				}
				key := map[string]any{}
				for _, column := range table.Columns {
					if column.PrimaryKey {
						key[column.Name] = row[column.Name]
					}
				}
				encoded, err := json.Marshal(key)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO gitseq_row_provenance VALUES (?,?,?,?,?,?)", position, record.ID, ordinal, name, string(encoded), operation.name); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO __gitseq_row_versions VALUES (?,?,1,?) ON CONFLICT(table_name,key_json) DO UPDATE SET version=version+1,event_id=excluded.event_id", name, string(encoded), record.ID); err != nil {
					return err
				}
				ordinal++
			}
		}
	}
	return nil
}

func (p *projection) advanceFixture(ctx context.Context, log host.Log) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if log.Genesis != p.frontier.Genesis || log.Depth != len(log.Records) || log.Head == "" || log.Depth < p.frontier.InterpretedPosition {
		return errors.New("log does not extend this projection")
	}
	if pos := p.frontier.InterpretedPosition; pos > 0 && log.Records[pos-1].ID != p.frontier.InterpretedEvent {
		return errors.New("interpreted prefix differs")
	}
	if _, err := p.db.ExecContext(ctx, "UPDATE gitseq_frontier SET verified_head=?,verified_depth=? WHERE singleton=1", log.Head, log.Depth); err != nil {
		return err
	}
	for index := p.frontier.InterpretedPosition; index < len(log.Records); index++ {
		record := log.Records[index]
		if err := p.step(ctx, record, index+1); err != nil {
			if jsonataddl.IsEvaluationTimeout(err) || ctx.Err() != nil {
				return errors.Join(err, p.loadFrontier(context.WithoutCancel(ctx)))
			}
			_, persistErr := p.db.ExecContext(ctx, "UPDATE gitseq_frontier SET gap_event=?,gap_reason=? WHERE singleton=1", record.ID, err.Error())
			return errors.Join(err, persistErr, p.loadFrontier(ctx))
		}
	}
	return p.loadFrontier(ctx)
}

func (p *projection) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var readErr error
	if p.reader != nil {
		readErr = p.reader.Close()
	}
	return errors.Join(readErr, p.db.Close())
}

func queryAuthorizer(app *jsonataddl.Application) jsonataddl.Authorizer {
	allowed := map[string]bool{"gitseq_decisions": true, "gitseq_facts": true, "gitseq_row_provenance": true, "gitseq_frontier": true, "gitseq_projection_identity": true}
	for name := range app.Tables() {
		allowed[strings.ToLower(name)] = true
	}
	for name := range app.Views() {
		allowed[strings.ToLower(name)] = true
	}
	return func(action sqlite3.AuthorizerActionCode, table, column, schema, inner string) sqlite3.AuthorizerReturnCode {
		switch action {
		case sqlite3.AUTH_SELECT:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_READ:
			if (schema == "main" || schema == "") && allowed[strings.ToLower(table)] {
				return sqlite3.AUTH_OK
			}
		case sqlite3.AUTH_FUNCTION:
			switch strings.ToLower(column) {
			case "count", "sum", "min", "max", "coalesce":
				return sqlite3.AUTH_OK
			}
		case sqlite3.AUTH_PRAGMA:
			if table == "query_only" && column == "" {
				return sqlite3.AUTH_OK
			}
		}
		return sqlite3.AUTH_DENY
	}
}

// QueryResult carries bounded rows and the verified/interpreted frontier.
type QueryResult struct {
	Frontier  frontier `json:"frontier"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}

func (p *projection) queryFixture(ctx context.Context, statement string) (QueryResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(statement) > 4096 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "SELECT ") || strings.Contains(statement, ";") {
		return QueryResult{}, errors.New("query must be one bounded SELECT")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := p.reader.QueryContext(ctx, statement)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return QueryResult{}, err
	}
	result := QueryResult{Frontier: p.frontier, Rows: [][]any{}, Columns: []string{}}
	for _, column := range columns {
		result.Columns = append(result.Columns, column.Name())
	}
	bytes := 0
	for rows.Next() {
		if len(result.Rows) == 256 {
			result.Truncated = true
			break
		}
		values, e := scanRow(rows, len(columns))
		if e != nil {
			return QueryResult{}, e
		}
		for i, column := range columns {
			values[i], err = queryValue(values[i], column.DatabaseTypeName())
			if err != nil {
				return QueryResult{}, err
			}
		}
		encoded, e := json.Marshal(values)
		if e != nil {
			return QueryResult{}, e
		}
		bytes += len(encoded)
		if bytes > 256<<10 {
			return QueryResult{}, errors.New("query result exceeds 256 KiB")
		}
		result.Rows = append(result.Rows, values)
	}
	return result, rows.Err()
}
