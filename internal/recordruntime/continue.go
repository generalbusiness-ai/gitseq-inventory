package recordruntime

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

// continueFixture is an explicit fixture delivery boundary. It keeps rows
// only when the core approves the complete writable-table shapes; a changed
// runtime or application requires openFixture's discard-and-replay path.
func (p *projection) continueFixture(ctx context.Context, next *jsonataddl.Application) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := validateHandle(next); err != nil {
		return err
	}
	if next.Name() != p.app.Name() || next.RuntimeProfile() != p.app.RuntimeProfile() {
		return errors.New("continue requires the same application and runtime")
	}
	if err := jsonataddl.ContinueCompatible(p.app, next); err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if err != nil && !committed {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err = refuseLegacyJSON(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT type,name FROM sqlite_master WHERE type IN ('view','index') AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type DESC,name")
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
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, statement := range drops {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	oldTables := p.app.Tables()
	tables := next.Tables()
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := oldTables[name]; !ok {
			if _, err = tx.ExecContext(ctx, tables[name].SQL); err != nil {
				return err
			}
		}
	}
	for _, statement := range next.ReplaceableSQL() {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err = p.saveIdentity(ctx, tx, next, p.frontier.Genesis); err != nil {
		return err
	}
	if err = p.checkpointAt("before-continue-commit"); err != nil {
		return err
	}
	// Prepare the replacement connection before committing: a connection
	// failure must not leave the new identity paired with a missing reader.
	reader, err := openDB(ctx, p.path, nil, true, next)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		reader.Close()
		return err
	}
	committed = true
	p.app = next
	previous := p.reader
	p.reader = reader
	// The transaction has committed; closing the previous reader cannot
	// roll it back. Leave the replacement usable even if close fails.
	closeErr := previous.Close()
	return closeErr
}
