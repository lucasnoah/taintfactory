package config

import (
	"os"
	"path/filepath"
)

// DataDir returns the base directory for factory state.
// Uses FACTORY_DATA_DIR env var if set, otherwise ~/.factory.
func DataDir() string {
	if v := os.Getenv("FACTORY_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".factory"
	}
	return filepath.Join(home, ".factory")
}
