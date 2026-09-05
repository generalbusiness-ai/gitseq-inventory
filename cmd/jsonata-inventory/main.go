// jsonata-inventory interprets only the fixed, verified inventory demonstration
// and executes one bounded read-only query. Production replay is refused.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/generalbusiness-ai/gitseq-inventory"
)

func main() {
	var repo, database, query string
	flag.StringVar(&repo, "repo", "", "sealed inventory-fixture demonstration repository (production logs refused)")
	flag.StringVar(&database, "database", "", "disposable SQLite fixture projection path")
	flag.StringVar(&query, "sql", "SELECT sku, available FROM stock ORDER BY sku", "read-only SQL query")
	flag.Parse()
	if repo == "" || database == "" {
		fmt.Fprintln(os.Stderr, "usage: jsonata-inventory -repo PATH -database PATH [-sql SELECT]")
		os.Exit(2)
	}
	ctx := context.Background()
	projection, err := inventory.OpenFixture(ctx, repo, database)
	if err != nil {
		fail(err)
	}
	defer projection.Close()
	result, err := projection.Query(ctx, query)
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
