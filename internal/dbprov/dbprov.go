// Package dbprov provisions per-repo PostgreSQL databases on a shared instance.
// It creates users, databases, and grants privileges idempotently.
package dbprov

import (
	"database/sql"

	"github.com/lucasnoah/taintfactory/internal/config"
)

// BuildProvisionSQL returns the SQL statements needed to provision a database.
// Statements must be run against the postgres maintenance database.
func BuildProvisionSQL(cfg *config.DatabaseConfig) []string {
	return buildProvisionSQL(cfg)
}

// Provision creates the database and user on the given admin connection.
// Idempotent: silently ignores "already exists" errors.
func Provision(adminConn *sql.DB, cfg *config.DatabaseConfig) error {
	return provision(adminConn, cfg)
}

// AdminConnStr takes a DATABASE_URL and returns a connection string
// pointing to the postgres maintenance database (overrides the database
// component to "postgres").
func AdminConnStr(databaseURL string) (string, error) {
	return adminConnStr(databaseURL)
}
