package checks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TypeScriptParser parses tsc --noEmit output.
type TypeScriptParser struct{}

type tsFinding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type tsResult struct {
	Errors   int         `json:"errors"`
	Findings []tsFinding `json:"findings"`
}

// tsc output format: src/auth.ts(42,5): error TS2345: Argument of type...
var tscLineRe = regexp.MustCompile(`^(.+)\((\d+),(\d+)\):\s+error\s+(TS\d+):\s+(.+)$`)

func (p *TypeScriptParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	var result tsResult

	// tsc outputs to stdout
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		m := tscLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		result.Findings = append(result.Findings, tsFinding{
			File:    m[1],
			Line:    lineNum,
			Column:  col,
			Code:    m[4],
			Message: m[5],
		})
		result.Errors++
	}

	passed := exitCode == 0
	summary := fmt.Sprintf("%d errors", result.Errors)
	if passed {
		summary = "no errors"
	}

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: result,
	}
}
