package checks

import "fmt"

// GenericParser is the fallback parser that captures exit code and output lengths.
type GenericParser struct{}

type genericResult struct {
	ExitCode     int `json:"exit_code"`
	StdoutLength int `json:"stdout_length"`
	StderrLength int `json:"stderr_length"`
}

func (p *GenericParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	passed := exitCode == 0
	summary := fmt.Sprintf("exit code %d, stdout=%d bytes, stderr=%d bytes", exitCode, len(stdout), len(stderr))
	if passed {
		summary = "passed (exit code 0)"
	}

	return ParseResult{
		Passed: passed,
		Summary: summary,
		Findings: genericResult{
			ExitCode:     exitCode,
			StdoutLength: len(stdout),
			StderrLength: len(stderr),
		},
	}
}
