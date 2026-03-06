package dbprov

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
)

// buildProvisionSQL returns the SQL statements to create a role and database.
func buildProvisionSQL(cfg *config.DatabaseConfig) []string {
	escapedPass := strings.ReplaceAll(cfg.Password, "'", "''")
	return []string{
		fmt.Sprintf(`CREATE ROLE "%s" WITH LOGIN PASSWORD '%s'`, cfg.User, escapedPass),
		fmt.Sprintf(`CREATE DATABASE "%s" OWNER "%s"`, cfg.Name, cfg.User),
		fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE "%s" TO "%s"`, cfg.Name, cfg.User),
	}
}

// provision executes the provisioning SQL against an admin connection.
// Idempotent: treats duplicate_object (42710) and duplicate_database (42P04) as success.
func provision(adminConn *sql.DB, cfg *config.DatabaseConfig) error {
	stmts := buildProvisionSQL(cfg)
	for _, stmt := range stmts {
		if _, err := adminConn.Exec(stmt); err != nil {
			if isAlreadyExists(err) {
				continue
			}
			return fmt.Errorf("dbprov: %w", err)
		}
	}
	return nil
}

// isAlreadyExists checks if the error is a PG duplicate_object (42710) or duplicate_database (42P04).
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "42710") || strings.Contains(msg, "42P04")
}

// adminConnStr parses a DATABASE_URL and overrides the path to /postgres.
func adminConnStr(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("dbprov: invalid DATABASE_URL: %w", err)
	}
	u.Path = "/postgres"
	return u.String(), nil
}
