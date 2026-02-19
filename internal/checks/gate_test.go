package checks

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRunGate_AllPass(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 0, Stdout: "ok"},
			{ExitCode: 0, Stdout: "ok"},
		},
	}
	runner := NewRunner(mock)

	gate, results, err := runner.RunGate("/tmp", GateOpts{
		Issue:    42,
		Stage:    "implement",
		FixRound: 0,
		Checks: []GateCheckConfig{
			{Name: "lint", Command: "npm run lint", Parser: "generic", Timeout: 30 * time.Second},
			{Name: "test", Command: "npm test", Parser: "generic", Timeout: 60 * time.Second},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gate.Passed {
		t.Error("expected gate to pass")
	}
	if gate.Gate != "implement" {
		t.Errorf("expected gate=implement, got %q", gate.Gate)
	}
	if gate.Issue != 42 {
		t.Errorf("expected issue=42, got %d", gate.Issue)
	}
	if len(gate.Checks) != 2 {
		t.Fatalf("expected 2 check results, got %d", len(gate.Checks))
	}
	if len(gate.RemainingFailures) != 0 {
		t.Errorf("expected no failures, got %d", len(gate.RemainingFailures))
	}
	if len(results) != 2 {
		t.Errorf("expected 2 raw results, got %d", len(results))
	}
}

func TestRunGate_StopOnFirstFailure(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 1, Stdout: "lint errors"},
		},
	}
	runner := NewRunner(mock)

	gate, results, err := runner.RunGate("/tmp", GateOpts{
		Issue:    42,
		Stage:    "implement",
		Continue: false,
		Checks: []GateCheckConfig{
			{Name: "lint", Command: "npm run lint", Parser: "generic"},
			{Name: "test", Command: "npm test", Parser: "generic"},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.Passed {
		t.Error("expected gate to fail")
	}
	// Should only have run 1 check (stopped on first failure)
	if len(gate.Checks) != 1 {
		t.Errorf("expected 1 check result (stopped on failure), got %d", len(gate.Checks))
	}
	if len(results) != 1 {
		t.Errorf("expected 1 raw result, got %d", len(results))
	}
	if _, ok := gate.RemainingFailures["lint"]; !ok {
		t.Error("expected lint in remaining failures")
	}
}

func TestRunGate_ContinueAfterFailure(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 1, Stdout: "lint errors"},
			{ExitCode: 0, Stdout: "tests ok"},
		},
	}
	runner := NewRunner(mock)

	gate, results, err := runner.RunGate("/tmp", GateOpts{
		Issue:    42,
		Stage:    "implement",
		Continue: true,
		Checks: []GateCheckConfig{
			{Name: "lint", Command: "npm run lint", Parser: "generic"},
			{Name: "test", Command: "npm test", Parser: "generic"},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.Passed {
		t.Error("expected gate to fail")
	}
	// Should have run both checks
	if len(gate.Checks) != 2 {
		t.Errorf("expected 2 check results (continue mode), got %d", len(gate.Checks))
	}
	if len(results) != 2 {
		t.Errorf("expected 2 raw results, got %d", len(results))
	}
	if !gate.Checks[1].Passed {
		t.Error("expected second check to pass")
	}
}

func TestRunGate_AutoFix(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 1, Stdout: "errors"},  // lint fails
			{ExitCode: 0},                     // fix command
			{ExitCode: 0, Stdout: "ok"},       // lint re-run passes
			{ExitCode: 0, Stdout: "tests ok"}, // test passes
		},
	}
	runner := NewRunner(mock)

	gate, _, err := runner.RunGate("/tmp", GateOpts{
		Issue: 42,
		Stage: "implement",
		Checks: []GateCheckConfig{
			{Name: "lint", Command: "npm run lint", Parser: "generic", AutoFix: true, FixCommand: "npm run lint -- --fix"},
			{Name: "test", Command: "npm test", Parser: "generic"},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gate.Passed {
		t.Error("expected gate to pass after auto-fix")
	}
	if !gate.Checks[0].AutoFixed {
		t.Error("expected lint to be auto-fixed")
	}
	if gate.Checks[0].Runs != 2 {
		t.Errorf("expected 2 runs for auto-fixed check, got %d", gate.Checks[0].Runs)
	}
}

func TestRunGate_FixRound(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{
			{ExitCode: 0},
		},
	}
	runner := NewRunner(mock)

	gate, _, err := runner.RunGate("/tmp", GateOpts{
		Issue:    42,
		Stage:    "implement",
		FixRound: 3,
		Checks: []GateCheckConfig{
			{Name: "lint", Command: "npm run lint", Parser: "generic"},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.FixRound != 3 {
		t.Errorf("expected fix_round=3, got %d", gate.FixRound)
	}
}

func TestRunGate_EmptyChecks(t *testing.T) {
	mock := &mockCmd{}
	runner := NewRunner(mock)

	gate, results, err := runner.RunGate("/tmp", GateOpts{
		Issue: 42,
		Stage: "implement",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gate.Passed {
		t.Error("expected gate with no checks to pass")
	}
	if len(gate.Checks) != 0 {
		t.Errorf("expected 0 check results, got %d", len(gate.Checks))
	}
	if len(results) != 0 {
		t.Errorf("expected 0 raw results, got %d", len(results))
	}
}

func TestGateResult_JSON(t *testing.T) {
	gate := &GateResult{
		Gate:     "implement",
		Issue:    42,
		FixRound: 0,
		Passed:   true,
		Checks: []GateCheckResult{
			{Check: "lint", Passed: true, Runs: 1},
		},
		RemainingFailures: map[string]GateFailure{},
	}

	jsonStr, err := gate.JSON()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jsonStr == "" {
		t.Error("expected non-empty JSON")
	}
	// Verify it's valid JSON by unmarshaling
	var parsed GateResult
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Gate != "implement" {
		t.Errorf("expected gate=implement, got %q", parsed.Gate)
	}
}
