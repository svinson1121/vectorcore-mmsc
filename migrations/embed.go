package migrations

import "embed"

// FS exposes SQL migrations for single-binary startup.
//
//go:embed postgres/*.sql sqlite/*.sql
var FS embed.FS
