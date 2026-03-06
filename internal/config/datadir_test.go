package config

import (
	"os"
	"testing"
)

func TestDataDir_Default(t *testing.T) {
	t.Setenv("FACTORY_DATA_DIR", "")
	dir := DataDir()
	home, _ := os.UserHomeDir()
	if dir != home+"/.factory" {
		t.Errorf("DataDir() = %q, want %q", dir, home+"/.factory")
	}
}

func TestDataDir_EnvOverride(t *testing.T) {
	t.Setenv("FACTORY_DATA_DIR", "/data")
	dir := DataDir()
	if dir != "/data" {
		t.Errorf("DataDir() = %q, want /data", dir)
	}
}
