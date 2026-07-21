// Package assets embeds the schemas and built-in profile build contexts so the
// cagent binary is self-contained.
package assets

import "embed"

// Files contains schemas and profiles/builtin.
//
//go:embed schemas/*.json all:profiles/builtin
var Files embed.FS
