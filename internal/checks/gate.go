package checks

import (
	"encoding/json"
	"fmt"
	"time"
)

// GateCheckResult holds the result of a single check within a gate run.
type GateCheckResult struct {
	Check     string `json:"check"`
	Passed    bool   `json:"passed"`
	AutoFixed bool   `json:"auto_fixed,omitempty"`
	Runs      int    `json:"runs"`
	Summary   string `json:"summary,omitempty"`
}

// GateFailure describes a remaining failure after a gate run.
type GateFailure struct {
	Count   int    `json:"count,omitempty"`
	Summary string `json:"summary"`
}

// GateResult is the structured output of a full gate run.
type GateResult struct {
	Gate              string                 `json:"gate"`
	Issue             int                    `json:"issue"`
	FixRound          int                    `json:"fix_round"`
	Passed            bool                   `json:"passed"`
	Checks            []GateCheckResult      `json:"checks"`
	RemainingFailures map[string]GateFailure `json:"remaining_failures,omitempty"`
}

// JSON returns the gate result as indented JSON.
func (g *GateResult) JSON() (string, error) {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GateOpts configures a gate run.
type GateOpts struct {
	Issue      int
	Stage      string
	FixRound   int
	Attempt    int
	Worktree   string
	Checks     []GateCheckConfig
	Continue   bool // run all checks even if some fail
}

// GateCheckConfig holds the config for a single check within a gate.
type GateCheckConfig struct {
	Name       string
	Command    string
	Parser     string
	Timeout    time.Duration
	AutoFix    bool
	FixCommand string
}

// RunGate executes all checks for a stage and returns a structured result.
// Each check result is also returned individually for DB logging.
func (r *Runner) RunGate(dir string, opts GateOpts) (*GateResult, []*Result, error) {
	gate := &GateResult{
		Gate:              opts.Stage,
		Issue:             opts.Issue,
		FixRound:          opts.FixRound,
		Passed:            true,
		RemainingFailures: make(map[string]GateFailure),
	}

	var allResults []*Result

	for _, chk := range opts.Checks {
		cfg := CheckConfig{
			Name:       chk.Name,
			Command:    chk.Command,
			Parser:     chk.Parser,
			Timeout:    chk.Timeout,
			AutoFix:    chk.AutoFix,
			FixCommand: chk.FixCommand,
		}

		result, err := r.Run(dir, cfg)
		if err != nil {
			return nil, allResults, fmt.Errorf("run check %q: %w", chk.Name, err)
		}
		allResults = append(allResults, result)

		runs := 1
		if result.AutoFixed {
			runs = 2
		}

		gc := GateCheckResult{
			Check:     chk.Name,
			Passed:    result.Passed,
			AutoFixed: result.AutoFixed,
			Runs:      runs,
			Summary:   result.Summary,
		}
		gate.Checks = append(gate.Checks, gc)

		if !result.Passed {
			gate.Passed = false
			gate.RemainingFailures[chk.Name] = GateFailure{
				Summary: result.Summary,
			}

			if !opts.Continue {
				break
			}
		}
	}

	return gate, allResults, nil
}
