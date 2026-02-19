package checks

import (
	"encoding/json"
	"fmt"
)

// VitestParser parses vitest/jest JSON reporter output.
type VitestParser struct{}

type vitestOutput struct {
	NumTotalTests  int               `json:"numTotalTests"`
	NumPassedTests int               `json:"numPassedTests"`
	NumFailedTests int               `json:"numFailedTests"`
	NumPendingTests int              `json:"numPendingTests"`
	TestResults    []vitestSuiteResult `json:"testResults"`
}

type vitestSuiteResult struct {
	Name        string              `json:"name"`
	Status      string              `json:"status"` // "passed" or "failed"
	AssertionResults []vitestAssertionResult `json:"assertionResults"`
}

type vitestAssertionResult struct {
	FullName       string   `json:"fullName"`
	Status         string   `json:"status"` // "passed", "failed"
	FailureMessages []string `json:"failureMessages"`
}

type vitestFailure struct {
	Suite string `json:"suite"`
	Test  string `json:"test"`
	Error string `json:"error"`
}

type vitestResult struct {
	Total      int             `json:"total"`
	Passed     int             `json:"passed"`
	Failed     int             `json:"failed"`
	Skipped    int             `json:"skipped"`
	DurationMs int             `json:"duration_ms"`
	Failures   []vitestFailure `json:"failures"`
}

func (p *VitestParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	var raw vitestOutput
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return ParseResult{
			Passed:  exitCode == 0,
			Summary: fmt.Sprintf("exit code %d (could not parse test JSON)", exitCode),
			Findings: vitestResult{
				Total:  -1,
				Failed: -1,
			},
		}
	}

	result := vitestResult{
		Total:   raw.NumTotalTests,
		Passed:  raw.NumPassedTests,
		Failed:  raw.NumFailedTests,
		Skipped: raw.NumPendingTests,
	}

	for _, suite := range raw.TestResults {
		for _, a := range suite.AssertionResults {
			if a.Status == "failed" {
				errMsg := ""
				if len(a.FailureMessages) > 0 {
					errMsg = a.FailureMessages[0]
				}
				result.Failures = append(result.Failures, vitestFailure{
					Suite: suite.Name,
					Test:  a.FullName,
					Error: errMsg,
				})
			}
		}
	}

	passed := exitCode == 0 && result.Failed == 0
	summary := fmt.Sprintf("%d passed, %d failed, %d skipped out of %d", result.Passed, result.Failed, result.Skipped, result.Total)

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: result,
	}
}
