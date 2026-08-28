// Package inventory binds this repository's reviewed SQL and JSONata files to
// the verified Gitseq application runtime.
package inventory

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/spike/jsonataddl"
)

// Application is the host identity for this standalone inventory package.
var Application = host.Application{
	Name:        "gitseq-inventory",
	FoldVersion: "jsonata-v206-sqlite-spike@0",
	SourceURL:   "https://github.com/generalbusiness-ai/gitseq-inventory.git",
}

//go:embed application.sql folds/inventory.jsonata
var sourceFiles embed.FS

// Load compiles the repository-local application package with the reviewed
// JSONata-with-DDL runtime pinned in go.mod.
func Load() (*jsonataddl.Profile, error) {
	return jsonataddl.Load(prefixedFS{sourceFiles}, "application", Application)
}

// prefixedFS presents the stable repository-root paths beneath the non-dot
// application root required by the reviewed loader. It changes no file bytes.
type prefixedFS struct{ fs.FS }

func (files prefixedFS) Open(name string) (fs.File, error) {
	const prefix = "application/"
	if !strings.HasPrefix(name, prefix) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return files.FS.Open(strings.TrimPrefix(name, prefix))
}
