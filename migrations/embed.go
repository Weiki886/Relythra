package migrations

import "embed"

// Files contains versioned PostgreSQL migrations.
//
//go:embed *.sql
var Files embed.FS
