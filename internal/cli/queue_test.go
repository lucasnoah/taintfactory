package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueueAddHelp_ConfigFlagPresent(t *testing.T) {
	out, err := executeCommand("queue", "add", "--help")
	if err != nil {
		t.Fatalf("queue add --help: %v", err)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("queue add --help does not mention --config flag:\n%s", out)
	}
}

func TestResolveConfigPath_FileNotFound(t *testing.T) {
	_, err := resolveConfigPath("/nonexistent/path/pipeline.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent config file, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestResolveConfigPath_Empty(t *testing.T) {
	got, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error for empty flag: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestResolveConfigPath_RelativeResolvesToAbsolute(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(cfgPath, []byte("pipeline:\n  name: test\n"), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	got, err := resolveConfigPath("pipeline.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	// Resolve symlinks for comparison (macOS /var → /private/var)
	wantResolved, _ := filepath.EvalSymlinks(cfgPath)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("got %q, want %q", got, cfgPath)
	}
}
