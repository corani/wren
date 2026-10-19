package wren

import "embed"

// Files contains all runtime web assets and vocabulary sources.
//
//go:embed web data/*.json
var Files embed.FS
