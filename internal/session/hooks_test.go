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

	// Should have 3 event types: UserPromptSubmit, Stop, SessionEnd
	if len(cfg.Hooks) != 3 {
		t.Fatalf("expected 3 event types, got %d", len(cfg.Hooks))
	}

	// Collect all commands by event name
	eventCmds := make(map[string]string)
	for event, groups := range cfg.Hooks {
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("event %q: expected 1 group with 1 handler", event)
		}
		eventCmds[event] = groups[0].Hooks[0].Command
	}

	// Verify expected events
	for _, ev := range []string{"UserPromptSubmit", "Stop", "SessionEnd"} {
		cmd, ok := eventCmds[ev]
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

	// Verify event types in commands
	if !strings.Contains(eventCmds["UserPromptSubmit"], "--event active") {
		t.Error("UserPromptSubmit hook should log 'active' event")
	}
	if !strings.Contains(eventCmds["Stop"], "--event idle") {
		t.Error("Stop hook should log 'idle' event")
	}
	if !strings.Contains(eventCmds["SessionEnd"], "--event exited") {
		t.Error("SessionEnd hook should log 'exited' event")
	}

	// Verify handler type is "command"
	for event, groups := range cfg.Hooks {
		if groups[0].Hooks[0].Type != "command" {
			t.Errorf("event %q: handler type = %q, want 'command'", event, groups[0].Hooks[0].Type)
		}
	}
}

func TestWriteHooksFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := GenerateHooksConfig("test-sess", 1, "plan")

	path, err := WriteHooksFile(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, ".claude", "settings.local.json")
	if path != expectedPath {
		t.Errorf("path = %q, want %q", path, expectedPath)
	}

	// Read back and verify structure
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	hooks, ok := got["hooks"]
	if !ok {
		t.Fatal("missing 'hooks' key in settings")
	}

	hooksMap, ok := hooks.(map[string]interface{})
	if !ok {
		t.Fatal("'hooks' should be a map")
	}

	if len(hooksMap) != 3 {
		t.Errorf("got %d event types, want 3", len(hooksMap))
	}
}

func TestWriteHooksFile_MergesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	// Write existing settings
	existing := `{"allowedTools": ["Read", "Write"], "other": true}`
	os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte(existing), 0o644)

	cfg := GenerateHooksConfig("merge-test", 1, "plan")
	_, err := WriteHooksFile(tmpDir, cfg)
	if err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.local.json"))
	var got map[string]interface{}
	json.Unmarshal(data, &got)

	// Hooks should be present
	if _, ok := got["hooks"]; !ok {
		t.Error("missing hooks after merge")
	}
	// Existing keys should be preserved
	if _, ok := got["allowedTools"]; !ok {
		t.Error("existing allowedTools key was lost during merge")
	}
	if _, ok := got["other"]; !ok {
		t.Error("existing 'other' key was lost during merge")
	}
}

func TestWriteHooksFile_CreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "deep", "project")
	cfg := GenerateHooksConfig("nested-test", 1, "plan")

	_, err := WriteHooksFile(nestedDir, cfg)
	if err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	info, err := os.Stat(filepath.Join(nestedDir, ".claude"))
	if err != nil {
		t.Fatalf("stat .claude dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected .claude to be a directory")
	}
}
