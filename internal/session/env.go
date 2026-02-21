package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadOAuthToken tries to read CLAUDE_CODE_OAUTH_TOKEN from the environment
// first, then falls back to ~/.factory/.env.
func loadOAuthToken() string {
	if v := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return readEnvFileVar(filepath.Join(home, ".factory", ".env"), "CLAUDE_CODE_OAUTH_TOKEN")
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
