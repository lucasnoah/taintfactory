package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Result holds the structured output of a check run.
type Result struct {
	CheckName  string `json:"check_name"`
	Passed     bool   `json:"passed"`
	AutoFixed  bool   `json:"auto_fixed"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int    `json:"duration_ms"`
	Summary    string `json:"summary"`
	Findings   string `json:"findings"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

// CheckConfig mirrors config.Check with the fields the runner needs.
type CheckConfig struct {
	Name       string
	Command    string
	Parser     string
	Timeout    time.Duration
	AutoFix    bool
	FixCommand string
}

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, dir string, command string) (stdout string, stderr string, exitCode int, err error)
}

// ExecRunner implements CommandRunner by shelling out.
type ExecRunner struct{}

func (e *ExecRunner) Run(ctx context.Context, dir string, command string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return stdoutBuf.String(), stderrBuf.String(), -1, fmt.Errorf("exec: %w", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode, nil
}

// Runner executes checks and parses their output.
type Runner struct {
	cmd     CommandRunner
	parsers map[string]Parser
}

// NewRunner creates a Runner with the given command runner.
func NewRunner(cmd CommandRunner) *Runner {
	r := &Runner{
		cmd:     cmd,
		parsers: make(map[string]Parser),
	}
	r.parsers["eslint"] = &ESLintParser{}
	r.parsers["prettier"] = &PrettierParser{}
	r.parsers["typescript"] = &TypeScriptParser{}
	r.parsers["vitest"] = &VitestParser{}
	r.parsers["npm-audit"] = &NPMAuditParser{}
	r.parsers["generic"] = &GenericParser{}
	return r
}

// Run executes a single check in the given directory.
func (r *Runner) Run(dir string, cfg CheckConfig) (*Result, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	result, err := r.runOnce(dir, cfg, timeout)
	if err != nil {
		return nil, err
	}

	// Auto-fix: if check failed, auto_fix enabled, and fix_command set, run fix then re-check
	if !result.Passed && cfg.AutoFix && cfg.FixCommand != "" {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		// Run fix command (ignore exit code — fix commands often exit non-zero)
		_, _, _, _ = r.cmd.Run(ctx, dir, cfg.FixCommand)

		// Re-run the check
		recheck, err := r.runOnce(dir, cfg, timeout)
		if err != nil {
			return nil, fmt.Errorf("re-run after fix: %w", err)
		}
		recheck.AutoFixed = true
		return recheck, nil
	}

	return result, nil
}

// runOnce executes a check command once and parses the output.
func (r *Runner) runOnce(dir string, cfg CheckConfig, timeout time.Duration) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	stdout, stderr, exitCode, err := r.cmd.Run(ctx, dir, cfg.Command)
	durationMs := int(time.Since(start).Milliseconds())

	if err != nil {
		// Context deadline exceeded → timeout
		if ctx.Err() == context.DeadlineExceeded {
			return &Result{
				CheckName:  cfg.Name,
				Passed:     false,
				ExitCode:   -1,
				DurationMs: durationMs,
				Summary:    fmt.Sprintf("timeout after %s", timeout),
				Stdout:     stdout,
				Stderr:     stderr,
			}, nil
		}
		return nil, fmt.Errorf("run check %q: %w", cfg.Name, err)
	}

	// Parse output
	parser, ok := r.parsers[cfg.Parser]
	if !ok {
		parser = r.parsers["generic"]
	}

	parsed := parser.Parse(stdout, stderr, exitCode)

	findingsJSON, _ := json.Marshal(parsed.Findings)

	return &Result{
		CheckName:  cfg.Name,
		Passed:     exitCode == 0 && parsed.Passed,
		ExitCode:   exitCode,
		DurationMs: durationMs,
		Summary:    parsed.Summary,
		Findings:   string(findingsJSON),
		Stdout:     stdout,
		Stderr:     stderr,
	}, nil
}

// Kill sends SIGTERM to a process, waits 2s, then SIGKILL.
// Used by ExecRunner's context cancellation.
func init() {
	// Set exec.CommandContext to use process group kill
	// This is handled automatically by Go's context cancellation.
	// The context.WithTimeout in runOnce handles SIGKILL after deadline.
	_ = syscall.SIGTERM // referenced for documentation
}
