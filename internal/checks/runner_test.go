package checks

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockCmd records calls and returns configured results.
type mockCmd struct {
	calls   []mockCall
	results []mockResult
	callIdx int
}

type mockCall struct {
	Dir     string
	Command string
}

type mockResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func (m *mockCmd) Run(ctx context.Context, dir string, command string) (string, string, int, error) {
	m.calls = append(m.calls, mockCall{Dir: dir, Command: command})
	if m.callIdx >= len(m.results) {
		return "", "", 0, nil
	}
	r := m.results[m.callIdx]
	m.callIdx++
	return r.Stdout, r.Stderr, r.ExitCode, r.Err
}

func TestRunner_Run_HappyPath(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{Stdout: "all good", ExitCode: 0},
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:    "lint",
		Command: "npm run lint",
		Parser:  "generic",
		Timeout: 30 * time.Second,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected passed=true, got false")
	}
	if result.CheckName != "lint" {
		t.Errorf("expected check_name=lint, got %q", result.CheckName)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit_code=0, got %d", result.ExitCode)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	if mock.calls[0].Dir != "/tmp/test" {
		t.Errorf("expected dir=/tmp/test, got %q", mock.calls[0].Dir)
	}
	if mock.calls[0].Command != "npm run lint" {
		t.Errorf("expected command=npm run lint, got %q", mock.calls[0].Command)
	}
}

func TestRunner_Run_FailedCheck(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{Stdout: "errors found", ExitCode: 1},
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:    "lint",
		Command: "npm run lint",
		Parser:  "generic",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Errorf("expected passed=false, got true")
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit_code=1, got %d", result.ExitCode)
	}
}

func TestRunner_Run_AutoFix(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{Stdout: "errors found", ExitCode: 1},   // initial run
			{Stdout: "fixed", ExitCode: 0},           // fix command
			{Stdout: "all good", ExitCode: 0},        // re-run
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:       "lint",
		Command:    "npm run lint",
		Parser:     "generic",
		AutoFix:    true,
		FixCommand: "npm run lint -- --fix",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected passed=true after fix, got false")
	}
	if !result.AutoFixed {
		t.Errorf("expected auto_fixed=true")
	}
	if len(mock.calls) != 3 {
		t.Fatalf("expected 3 calls (run, fix, re-run), got %d", len(mock.calls))
	}
	if mock.calls[1].Command != "npm run lint -- --fix" {
		t.Errorf("expected fix command, got %q", mock.calls[1].Command)
	}
}

func TestRunner_Run_AutoFixStillFails(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 1},  // initial run
			{ExitCode: 0},  // fix command
			{ExitCode: 1},  // re-run still fails
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:       "lint",
		Command:    "npm run lint",
		Parser:     "generic",
		AutoFix:    true,
		FixCommand: "npm run lint -- --fix",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Errorf("expected passed=false even after fix attempt")
	}
	if !result.AutoFixed {
		t.Errorf("expected auto_fixed=true (fix was attempted)")
	}
}

func TestRunner_Run_NoAutoFixWhenPassing(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 0},
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:       "lint",
		Command:    "npm run lint",
		Parser:     "generic",
		AutoFix:    true,
		FixCommand: "npm run lint -- --fix",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected passed=true")
	}
	if len(mock.calls) != 1 {
		t.Errorf("expected 1 call (no fix needed), got %d", len(mock.calls))
	}
}

func TestRunner_Run_UnknownParserFallsToGeneric(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{Stdout: "output", ExitCode: 0},
		},
	}
	runner := NewRunner(mock)

	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:    "custom",
		Command: "custom-check",
		Parser:  "unknown-parser",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected passed=true")
	}
	if result.Summary != "passed (exit code 0)" {
		t.Errorf("expected generic summary, got %q", result.Summary)
	}
}

func TestRunner_Run_CommandError(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{Err: fmt.Errorf("connection refused")},
		},
	}
	runner := NewRunner(mock)

	_, err := runner.Run("/tmp/test", CheckConfig{
		Name:    "lint",
		Command: "npm run lint",
		Parser:  "generic",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRunner_Run_DefaultTimeout(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 0},
		},
	}
	runner := NewRunner(mock)

	// Timeout = 0 should use default (2 minutes)
	result, err := runner.Run("/tmp/test", CheckConfig{
		Name:    "lint",
		Command: "npm run lint",
		Parser:  "generic",
		Timeout: 0,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected passed=true")
	}
}
