// Package migrations embeds the SQL schema migrations so they ship inside the binaries.
//
// Files follow the golang-migrate naming scheme: NNNNNN_description.up.sql and
// NNNNNN_description.down.sql. Create new pairs with `go run ./cmd/migrate create NAME`.
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
