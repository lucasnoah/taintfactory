package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// HookHandler represents a single hook handler within an event group.
type HookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookGroup represents a group of hooks for a single event, with an optional matcher.
type HookGroup struct {
	Hooks []HookHandler `json:"hooks"`
}

// HooksConfig is the Claude Code settings structure containing hooks.
// Written to .claude/settings.local.json.
type HooksConfig struct {
	Hooks map[string][]HookGroup `json:"hooks"`
}

// GenerateHooksConfig builds a Claude Code hooks config that calls factory event log
// for each lifecycle event.
//
// Event mapping:
//   - Stop → idle (Claude finished processing, waiting for input)
//   - UserPromptSubmit → active (prompt submitted, Claude is processing)
//   - SessionEnd → exited (session terminated)
func GenerateHooksConfig(sessionName string, issue int, stage string) *HooksConfig {
	factoryBin := resolveFactoryBinary()
	base := fmt.Sprintf("%s event log --session %s --issue %d --stage %s", factoryBin, sessionName, issue, stage)

	return &HooksConfig{
		Hooks: map[string][]HookGroup{
			"UserPromptSubmit": {
				{Hooks: []HookHandler{{Type: "command", Command: base + " --event active"}}},
			},
			"Stop": {
				{Hooks: []HookHandler{{Type: "command", Command: base + " --event idle"}}},
			},
			"SessionEnd": {
				{Hooks: []HookHandler{{Type: "command", Command: base + " --event exited"}}},
			},
		},
	}
}

// resolveFactoryBinary returns the absolute path to the factory binary.
// Uses os.Executable() first, falling back to "factory" (assumes PATH).
func resolveFactoryBinary() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.EvalSymlinks(exe); err == nil {
			return abs
		}
		return exe
	}
	return "factory"
}

// WriteHooksFile writes the hooks config to <workdir>/.claude/settings.local.json.
// If the file already exists, it reads it and merges the hooks key.
// Creates the .claude directory if it doesn't exist.
func WriteHooksFile(workdir string, cfg *HooksConfig) (string, error) {
	dir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create .claude dir: %w", err)
	}

	path := filepath.Join(dir, "settings.local.json")

	// Read existing settings if present and merge
	existing := make(map[string]interface{})
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	// Set the hooks key
	existing["hooks"] = cfg.Hooks

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write settings file: %w", err)
	}
	return path, nil
}
