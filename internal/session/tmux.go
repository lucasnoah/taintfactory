package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// TmuxRunner abstracts tmux shell commands for testability.
type TmuxRunner interface {
	NewSession(name string) error
	SendKeys(session string, keys string) error
	KillSession(name string) error
	CapturePane(name string) (string, error)
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
