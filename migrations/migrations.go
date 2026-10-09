// Package migrations bettet die SQL-Migrationen in das Binary ein.
package migrations

import "embed"

// FS enthält alle Migrationsdateien.
//
//go:embed *.sql
var FS embed.FS
