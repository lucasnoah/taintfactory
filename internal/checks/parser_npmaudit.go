package checks

import (
	"encoding/json"
	"fmt"
)

// NPMAuditParser parses npm audit --json output.
type NPMAuditParser struct{}

type npmAuditOutput struct {
	Metadata struct {
		Vulnerabilities struct {
			Critical int `json:"critical"`
			High     int `json:"high"`
			Moderate int `json:"moderate"`
			Low      int `json:"low"`
			Info     int `json:"info"`
			Total    int `json:"total"`
		} `json:"vulnerabilities"`
	} `json:"metadata"`
	Vulnerabilities map[string]npmVulnerability `json:"vulnerabilities"`
}

type npmVulnerability struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Via      json.RawMessage `json:"via"`
}

type auditAdvisory struct {
	Severity string `json:"severity"`
	Module   string `json:"module"`
	Title    string `json:"title"`
}

type auditResult struct {
	Total      int             `json:"total"`
	Critical   int             `json:"critical"`
	High       int             `json:"high"`
	Moderate   int             `json:"moderate"`
	Low        int             `json:"low"`
	Advisories []auditAdvisory `json:"advisories"`
}

func (p *NPMAuditParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	var raw npmAuditOutput
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return ParseResult{
			Passed:  exitCode == 0,
			Summary: fmt.Sprintf("exit code %d (could not parse npm audit JSON)", exitCode),
			Findings: auditResult{
				Total: -1,
			},
		}
	}

	v := raw.Metadata.Vulnerabilities
	result := auditResult{
		Total:    v.Total,
		Critical: v.Critical,
		High:     v.High,
		Moderate: v.Moderate,
		Low:      v.Low,
	}

	for name, vuln := range raw.Vulnerabilities {
		result.Advisories = append(result.Advisories, auditAdvisory{
			Severity: vuln.Severity,
			Module:   name,
			Title:    vuln.Title,
		})
	}

	passed := exitCode == 0
	summary := fmt.Sprintf("%d vulnerabilities (%d critical, %d high, %d moderate, %d low)",
		v.Total, v.Critical, v.High, v.Moderate, v.Low)
	if passed {
		summary = "no vulnerabilities found"
	}

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: result,
	}
}
