// Package migrations embeds the SQL migrations into the binary.
package migrations

import "embed"

// FS holds the SQL migration files.
//
//go:embed *.sql
var FS embed.FS
