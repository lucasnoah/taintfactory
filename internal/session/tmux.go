package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TmuxRunner abstracts tmux shell commands for testability.
type TmuxRunner interface {
	NewSession(name string) error
	SendKeys(session string, keys string) error
	SendBuffer(session string, content string) error
	KillSession(name string) error
	CapturePane(name string) (string, error)
	CapturePaneLines(name string, lines int) (string, error)
	ListSessions() ([]string, error)
	HasSession(name string) (bool, error)
}

// ExecTmux implements TmuxRunner by shelling out to tmux.
type ExecTmux struct{}

// NewExecTmux returns a new ExecTmux.
func NewExecTmux() *ExecTmux {
	return &ExecTmux{}
}

func (e *ExecTmux) NewSession(name string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name).Run()
}

func (e *ExecTmux) SendKeys(session string, keys string) error {
	return exec.Command("tmux", "send-keys", "-t", session, keys, "Enter").Run()
}

// SendBuffer writes content to a temp file, loads it into a tmux buffer,
// pastes it into the target session, and submits with Enter.
// This handles multiline prompts correctly for interactive Claude Code sessions.
func (e *ExecTmux) SendBuffer(session string, content string) error {
	// Write content to a temp file
	f, err := os.CreateTemp("", "factory-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	f.Close()

	// Load into tmux buffer
	if err := exec.Command("tmux", "load-buffer", f.Name()).Run(); err != nil {
		return fmt.Errorf("load-buffer: %w", err)
	}

	// Paste into target session
	if err := exec.Command("tmux", "paste-buffer", "-t", session).Run(); err != nil {
		return fmt.Errorf("paste-buffer: %w", err)
	}

	// Wait for the terminal to finish processing the pasted content
	// before sending Enter. Large pastes need time to be received.
	time.Sleep(1 * time.Second)

	// Submit with Enter
	return exec.Command("tmux", "send-keys", "-t", session, "Enter").Run()
}

func (e *ExecTmux) KillSession(name string) error {
	return exec.Command("tmux", "kill-session", "-t", name).Run()
}

func (e *ExecTmux) CapturePane(name string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", name, "-p", "-S", "-").Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	return string(out), nil
}

func (e *ExecTmux) CapturePaneLines(name string, lines int) (string, error) {
	startLine := fmt.Sprintf("-%d", lines)
	out, err := exec.Command("tmux", "capture-pane", "-t", name, "-p", "-S", startLine).Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	return string(out), nil
}

func (e *ExecTmux) ListSessions() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux exits non-zero when no server is running
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("list-sessions: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

func (e *ExecTmux) HasSession(name string) (bool, error) {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("has-session: %w", err)
}
