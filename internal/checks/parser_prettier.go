package checks

import (
	"fmt"
	"strings"
)

// PrettierParser parses prettier --check output.
type PrettierParser struct{}

type prettierResult struct {
	FilesNeedingFormat []string `json:"files_needing_format"`
	Count              int      `json:"count"`
}

func (p *PrettierParser) Parse(stdout string, stderr string, exitCode int) ParseResult {
	// prettier --check outputs lines like:
	// Checking formatting...
	// [warn] src/auth.ts
	// [warn] src/index.ts
	// [warn] Code style issues found in the above file(s). Forgot to run Prettier?

	var files []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[warn] ") {
			file := strings.TrimPrefix(line, "[warn] ")
			// Skip summary lines
			if !strings.Contains(file, "Code style issues") && !strings.Contains(file, "Forgot to run") {
				files = append(files, file)
			}
		}
	}

	result := prettierResult{
		FilesNeedingFormat: files,
		Count:              len(files),
	}

	passed := exitCode == 0
	summary := fmt.Sprintf("%d files need formatting", len(files))
	if passed {
		summary = "all files formatted"
	}

	return ParseResult{
		Passed:   passed,
		Summary:  summary,
		Findings: result,
	}
}
