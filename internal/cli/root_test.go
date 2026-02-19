package cli

import (
	"bytes"
	"strings"
	"testing"
)

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestVersionCommand(t *testing.T) {
	SetVersion("test-version")
	out, err := executeCommand("version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "test-version") {
		t.Errorf("expected version output to contain 'test-version', got: %s", out)
	}
}

func TestRootHelp(t *testing.T) {
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSubcommands := []string{
		"pipeline", "session", "check", "context", "event",
		"status", "analytics", "worktree", "pr", "config",
		"db", "qa", "stage", "version",
	}
	for _, sub := range expectedSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}

func TestPipelineSubcommands(t *testing.T) {
	subcmds := []string{"create", "advance", "status", "retry", "fail", "abort"}
	for _, sub := range subcmds {
		out, err := executeCommand("pipeline", sub, "--help")
		if err != nil {
			t.Errorf("pipeline %s --help failed: %v", sub, err)
		}
		if out == "" {
			t.Errorf("pipeline %s --help produced no output", sub)
		}
	}
}

func TestSessionSubcommands(t *testing.T) {
	subcmds := []string{"create", "kill", "list", "send", "steer", "peek", "status", "wait-idle"}
	for _, sub := range subcmds {
		out, err := executeCommand("session", sub, "--help")
		if err != nil {
			t.Errorf("session %s --help failed: %v", sub, err)
		}
		if out == "" {
			t.Errorf("session %s --help produced no output", sub)
		}
	}
}

func TestCheckSubcommands(t *testing.T) {
	subcmds := []string{"run", "gate", "result", "history"}
	for _, sub := range subcmds {
		out, err := executeCommand("check", sub, "--help")
		if err != nil {
			t.Errorf("check %s --help failed: %v", sub, err)
		}
		if out == "" {
			t.Errorf("check %s --help produced no output", sub)
		}
	}
}

func TestContextSubcommands(t *testing.T) {
	subcmds := []string{"build", "checkpoint", "read", "render"}
	for _, sub := range subcmds {
		out, err := executeCommand("context", sub, "--help")
		if err != nil {
			t.Errorf("context %s --help failed: %v", sub, err)
		}
		if out == "" {
			t.Errorf("context %s --help produced no output", sub)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, err := executeCommand("nonexistent")
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	}
}
