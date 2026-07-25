// Package migrations holds notification-service's versioned SQL schema migrations,
// embedded into the binary so the service applies its own schema at boot
// (see shared/database.RunMigrations). Files are applied in numeric order and
// recorded in goose_db_version_notification_service.
package migrations

import "embed"

// FS carries every .sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
