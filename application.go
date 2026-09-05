// Package inventory binds the reviewed SQL and JSONata sources to a sealed
// demonstration. Production replay is unavailable until the evaluator has
// deterministic step and allocation bounds.
package inventory

import (
	"context"
	"embed"

	"github.com/generalbusiness-ai/gitseq-inventory/internal/recordruntime"
	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

//go:embed application.sql folds/inventory.jsonata folds/normalize.jsonata
var sourceFiles embed.FS

// Binding selects the exact nine-component runtime. Source revisions are
// recorded separately in each disposable projection.
func Binding() (host.Application, error) {
	identity, err := recordruntime.Identity()
	if err != nil {
		return host.Application{}, err
	}
	return host.Application{
		Name: "gitseq-inventory", FoldVersion: identity.Digest(),
		SourceURL: "https://github.com/generalbusiness-ai/gitseq-inventory.git",
	}, nil
}

// Load compiles this repository's embedded sources with the shared core.
// Compilation alone grants no production replay capability.
func Load() (*jsonataddl.Application, error) {
	return recordruntime.LoadSource(sourceFiles, "gitseq-inventory")
}

// OpenFixture verifies the binding and signatures, admits only the documented
// three-event example, then builds or reuses its disposable projection.
func OpenFixture(ctx context.Context, repo, database string) (*recordruntime.Fixture, error) {
	binding, err := Binding()
	if err != nil {
		return nil, err
	}
	workspace, err := host.Open(ctx, repo, binding)
	if err != nil {
		return nil, err
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		return nil, err
	}
	app, err := Load()
	if err != nil {
		return nil, err
	}
	return recordruntime.BuildFixture(ctx, app, log, database)
}
