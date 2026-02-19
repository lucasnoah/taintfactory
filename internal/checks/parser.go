package checks

// ParseResult holds the normalized output from a parser.
type ParseResult struct {
	Passed   bool        `json:"passed"`
	Summary  string      `json:"summary"`
	Findings interface{} `json:"findings"`
}

// Parser converts raw command output into a structured ParseResult.
type Parser interface {
	Parse(stdout string, stderr string, exitCode int) ParseResult
}
