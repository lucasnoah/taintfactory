package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// HookEntry represents a single Claude Code hook.
type HookEntry struct {
	Event   string `json:"event"`
	Command string `json:"command"`
}

// HooksConfig is the top-level hooks.json structure.
type HooksConfig struct {
	Hooks []HookEntry `json:"hooks"`
}

// GenerateHooksConfig builds a hooks config that calls factory event log
// for each Claude Code lifecycle event.
func GenerateHooksConfig(sessionName string, issue int, stage string) *HooksConfig {
	base := fmt.Sprintf("factory event log --session %s --issue %d --stage %s", sessionName, issue, stage)
	return &HooksConfig{
		Hooks: []HookEntry{
			{Event: "on_active", Command: base + " --event active"},
			{Event: "on_idle", Command: base + " --event idle"},
			{Event: "on_exit", Command: base + " --event exited"},
		},
	}
}

// WriteHooksFile writes the hooks config to <workdir>/.claude/hooks.json.
// Creates the .claude directory if it doesn't exist.
func WriteHooksFile(workdir string, cfg *HooksConfig) (string, error) {
	dir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create .claude dir: %w", err)
	}

	path := filepath.Join(dir, "hooks.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal hooks config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write hooks file: %w", err)
	}
	return path, nil
}
