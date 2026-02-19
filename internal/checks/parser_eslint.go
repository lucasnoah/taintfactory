package checks

import (
	"encoding/json"
	"fmt"
)

// ESLintParser parses ESLint JSON output.
type ESLintParser struct{}

type eslintFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

type eslintMessage struct {
	RuleID   string `json:"ruleId"`
	Severity int    `json:"severity"` // 1=warning, 2=error
	Message  string `json:"message"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Fix      *struct {
		Range [2]int `json:"range"`
		Text  string `json:"text"`
	} `json:"fix"`
}

type eslintFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type eslintResult struct {
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Fixable  int             `json:"fixable"`
	Findings []eslintFinding `json:"findings"`
}

func (p *ESLintParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	var files []eslintFile
	if err := json.Unmarshal([]byte(stdout), &files); err != nil {
		return ParseResult{
			Passed:  exitCode == 0,
			Summary: fmt.Sprintf("exit code %d (could not parse ESLint JSON)", exitCode),
			Findings: eslintResult{
				Errors: -1,
			},
		}
	}

	var result eslintResult
	for _, f := range files {
		for _, m := range f.Messages {
			sev := "warning"
			if m.Severity == 2 {
				sev = "error"
				result.Errors++
			} else {
				result.Warnings++
			}
			if m.Fix != nil {
				result.Fixable++
			}
			result.Findings = append(result.Findings, eslintFinding{
				File:     f.FilePath,
				Line:     m.Line,
				Column:   m.Column,
				Severity: sev,
				Rule:     m.RuleID,
				Message:  m.Message,
			})
		}
	}

	passed := result.Errors == 0
	summary := fmt.Sprintf("%d errors, %d warnings, %d fixable", result.Errors, result.Warnings, result.Fixable)

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: result,
	}
}
