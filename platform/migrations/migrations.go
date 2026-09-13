// Package migrations embeds the goose SQL migrations so binaries
// (yuqing-cli) can run them without a migrations directory on disk.
package migrations

import "embed"

// FS embeds platform and tenant migration SQL.
//go:embed platform/*.sql tenant/*.sql
var FS embed.FS
