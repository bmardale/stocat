package migrations

import "embed"

// Files contains the goose SQL migrations.
//
//go:embed *.sql
var Files embed.FS
