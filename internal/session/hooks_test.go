package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateHooksConfig(t *testing.T) {
	cfg := GenerateHooksConfig("42-impl", 42, "impl")

	if len(cfg.Hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(cfg.Hooks))
	}

	events := map[string]string{}
	for _, h := range cfg.Hooks {
		events[h.Event] = h.Command
	}

	// Verify all expected events present
	for _, ev := range []string{"on_active", "on_idle", "on_exit"} {
		cmd, ok := events[ev]
		if !ok {
			t.Errorf("missing hook for event %q", ev)
			continue
		}
		if !strings.Contains(cmd, "--session 42-impl") {
			t.Errorf("hook %q missing session name: %q", ev, cmd)
		}
		if !strings.Contains(cmd, "--issue 42") {
			t.Errorf("hook %q missing issue: %q", ev, cmd)
		}
		if !strings.Contains(cmd, "--stage impl") {
			t.Errorf("hook %q missing stage: %q", ev, cmd)
		}
	}

	// Verify event types in commands match
	if !strings.Contains(events["on_active"], "--event active") {
		t.Error("on_active hook should log 'active' event")
	}
	if !strings.Contains(events["on_idle"], "--event idle") {
		t.Error("on_idle hook should log 'idle' event")
	}
	if !strings.Contains(events["on_exit"], "--event exited") {
		t.Error("on_exit hook should log 'exited' event")
	}
}

func TestWriteHooksFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := GenerateHooksConfig("test-sess", 1, "plan")

	path, err := WriteHooksFile(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, ".claude", "hooks.json")
	if path != expectedPath {
		t.Errorf("path = %q, want %q", path, expectedPath)
	}

	// Read back and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hooks file: %v", err)
	}

	var got HooksConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal hooks: %v", err)
	}

	if len(got.Hooks) != 3 {
		t.Errorf("got %d hooks, want 3", len(got.Hooks))
	}
}

func TestWriteHooksFile_CreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "deep", "project")
	cfg := &HooksConfig{Hooks: []HookEntry{{Event: "on_idle", Command: "echo test"}}}

	_, err := WriteHooksFile(nestedDir, cfg)
	if err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	// Verify .claude dir was created
	info, err := os.Stat(filepath.Join(nestedDir, ".claude"))
	if err != nil {
		t.Fatalf("stat .claude dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected .claude to be a directory")
	}
}
