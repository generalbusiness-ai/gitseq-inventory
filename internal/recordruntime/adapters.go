package recordruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/generalbusiness-ai/tailapps/jsonataddl"
	"github.com/ncruces/go-sqlite3"
)

// Only the single writer's read-plan interval seats this authorizer. Query
// connections have a separate policy and never share this seat.
type readGuard struct {
	seated atomic.Pointer[jsonataddl.Authorizer]
}

func (g *readGuard) check(action sqlite3.AuthorizerActionCode, table, column, schema, inner string) sqlite3.AuthorizerReturnCode {
	if guard := g.seated.Load(); guard != nil {
		return (*guard)(action, table, column, schema, inner)
	}
	return sqlite3.AUTH_OK
}

func readInput(ctx context.Context, tx *sql.Tx, app *jsonataddl.Application, guard *readGuard, program string, event map[string]any) (jsonataddl.EvaluationInput, error) {
	input := jsonataddl.EvaluationInput{Meta: map[string]any{}, Event: event, Rows: map[string]any{}}
	if err := app.ValidateProgramInput(program, input.Meta, input.Event); err != nil {
		return jsonataddl.EvaluationInput{}, err
	}
	plan, ok := app.ReadPlan(program)
	if !ok {
		return jsonataddl.EvaluationInput{}, fmt.Errorf("unknown program %q", program)
	}
	authorizer, ok := app.ReadAuthorizer(program)
	if !ok {
		return jsonataddl.EvaluationInput{}, errors.New("program has no read authorizer")
	}
	guard.seated.Store(&authorizer)
	defer guard.seated.Store(nil)
	for _, read := range plan {
		value, err := executeRead(ctx, tx, read, event)
		if err != nil {
			return jsonataddl.EvaluationInput{}, fmt.Errorf("read %s: %w", read.Name, err)
		}
		input.Rows[read.Name] = value
	}
	return input, nil
}

func executeRead(ctx context.Context, tx *sql.Tx, read jsonataddl.Read, event map[string]any) (any, error) {
	args := make([]any, len(read.Parameters))
	for i, name := range read.Parameters {
		value, ok := event[name]
		if !ok {
			return nil, fmt.Errorf("event parameter %q is absent", name)
		}
		args[i] = jsonataddl.SQLiteBindValue(value, "")
	}
	rows, err := tx.QueryContext(ctx, read.SQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	values := []map[string]any{}
	limit := 1
	if read.Cardinality == jsonataddl.Many {
		limit = read.Limit
	}
	for rows.Next() {
		if len(values) == limit {
			return nil, fmt.Errorf("%s returned too many rows", read.Cardinality)
		}
		scanned, err := scanRow(rows, len(columns))
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column.Name()], err = jsonataddl.ReadRowValue(scanned[i], jsonataddl.LogicalType(column.DatabaseTypeName()))
			if err != nil {
				return nil, err
			}
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch read.Cardinality {
	case jsonataddl.One:
		if len(values) != 1 {
			return nil, errors.New("ONE returned no rows")
		}
		return values[0], nil
	case jsonataddl.OptionalOne:
		if len(values) == 0 {
			return nil, nil
		}
		return values[0], nil
	case jsonataddl.Many:
		return values, nil
	default:
		return nil, errors.New("unknown read cardinality")
	}
}

func scanRow(rows *sql.Rows, count int) ([]any, error) {
	values, destinations := make([]any, count), make([]any, count)
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	// The driver's BOOLEAN hint produces Go bool when scanning into any.
	// Restore the integer representation expected by the core's SQLite
	// codecs; the core still owns the logical boolean conversion.
	for i, value := range values {
		if boolean, ok := value.(bool); ok {
			values[i] = int64(0)
			if boolean {
				values[i] = int64(1)
			}
		}
	}
	return values, nil
}

func queryValue(value any, declared string) (any, error) {
	var column jsonataddl.SQLiteColumn
	switch value := value.(type) {
	case nil:
		column.Kind = jsonataddl.ColumnNull
	case int64:
		column.Kind, column.Int = jsonataddl.ColumnInteger, value
	case float64:
		column.Kind, column.Float = jsonataddl.ColumnFloat, value
	case string:
		column.Kind, column.Text = jsonataddl.ColumnText, value
	case []byte:
		column.Kind, column.Blob = jsonataddl.ColumnBlob, value
	default:
		return nil, fmt.Errorf("unsupported SQLite value %T", value)
	}
	return jsonataddl.LogicalColumnValue(column, jsonataddl.LogicalType(declared))
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func writeRow(ctx context.Context, tx *sql.Tx, table jsonataddl.Table, operation string, row map[string]any) error {
	if operation == "delete" {
		terms, args := []string{}, []any{}
		for _, key := range table.PrimaryKey {
			for _, column := range table.Columns {
				if strings.EqualFold(column.Name, key) {
					terms = append(terms, quote(column.Name)+"=?")
					args = append(args, jsonataddl.SQLiteBindValue(row[column.Name], column.Type))
				}
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM "+quote(table.Name)+" WHERE "+strings.Join(terms, " AND "), args...)
		return err
	}
	columns, placeholders, updates := []string{}, []string{}, []string{}
	args := []any{}
	for _, column := range table.Columns {
		columns = append(columns, quote(column.Name))
		placeholders = append(placeholders, "?")
		args = append(args, jsonataddl.SQLiteBindValue(row[column.Name], column.Type))
		if !column.PrimaryKey {
			updates = append(updates, quote(column.Name)+"=excluded."+quote(column.Name))
		}
	}
	statement := "INSERT INTO " + quote(table.Name) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	if operation == "upsert" {
		keys := make([]string, len(table.PrimaryKey))
		for i, key := range table.PrimaryKey {
			keys[i] = quote(key)
		}
		statement += " ON CONFLICT (" + strings.Join(keys, ",") + ") DO "
		if len(updates) == 0 {
			statement += "NOTHING"
		} else {
			statement += "UPDATE SET " + strings.Join(updates, ",")
		}
	}
	_, err := tx.ExecContext(ctx, statement, args...)
	return err
}
