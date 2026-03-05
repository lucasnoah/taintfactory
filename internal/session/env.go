package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
)

// loadOAuthToken tries to read CLAUDE_CODE_OAUTH_TOKEN from the environment
// first, then falls back to {datadir}/.env.
// Respects FACTORY_DATA_DIR env var (default: ~/.factory).
func loadOAuthToken() string {
	if v := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); v != "" {
		return v
	}
	return readEnvFileVar(filepath.Join(config.DataDir(), ".env"), "CLAUDE_CODE_OAUTH_TOKEN")
}

// readEnvFileVar reads the value of a specific key from a .env file.
// Supports both "KEY=VALUE" and "export KEY=VALUE" formats.
// Returns empty string if the file or key is not found.
func readEnvFileVar(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == key {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}
