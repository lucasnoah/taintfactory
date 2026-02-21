package checks

import "fmt"

// GenericParser is the fallback parser that captures exit code and actual output.
type GenericParser struct{}

// maxOutputLen caps how much stdout/stderr the generic parser retains in findings.
const maxOutputLen = 8000

func (p *GenericParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	passed := exitCode == 0
	summary := fmt.Sprintf("exit code %d, stdout=%d bytes, stderr=%d bytes", exitCode, len(stdout), len(stderr))
	if passed {
		summary = "passed (exit code 0)"
	}

	// For failures, include actual output so agents can see the errors.
	findings := ""
	if !passed {
		combined := stdout
		if stderr != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += stderr
		}
		// Keep the tail — error summaries and tracebacks are usually at the end.
		if len(combined) > maxOutputLen {
			combined = "…(truncated)\n" + combined[len(combined)-maxOutputLen:]
		}
		findings = combined
	}

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: findings,
	}
}
