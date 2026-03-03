package discord

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursor_DefaultsToZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor.json")

	c, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("LoadCursor on missing file: %v", err)
	}
	if c.LastEventID != 0 {
		t.Errorf("LastEventID = %d, want 0 for new cursor", c.LastEventID)
	}
}

func TestCursor_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor.json")

	c, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}

	c.LastEventID = 42
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("LoadCursor after save: %v", err)
	}
	if c2.LastEventID != 42 {
		t.Errorf("LastEventID = %d, want 42", c2.LastEventID)
	}
}

func TestCursor_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "cursor.json")

	c, err := LoadCursor(path)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	c.LastEventID = 7
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("cursor file not created: %v", err)
	}
}
