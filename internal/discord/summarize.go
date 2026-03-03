package discord

import (
	"fmt"
	"os/exec"
	"strings"
)

// BuildSummaryPrompt returns the claude --print prompt for a given stage type.
// Returns empty string for stages that use static messages (verify, merge).
func BuildSummaryPrompt(stage, sessionLog, gitDiff string) string {
	var instruction string
	switch stage {
	case "implement":
		instruction = `In 2-3 sentences: what was implemented, which files were created or modified, and what tests were added. Be specific about function names or schema changes. Format: 'Summary: <what was done>\nChanges: <specific changes>\nOpen Questions: <any or —>'`
	case "review":
		instruction = `In 2-3 sentences: what did the reviewer flag or change, and why? Highlight any bugs caught or design decisions reconsidered. List any open questions or TODOs left unresolved. Format: 'Summary: <what was reviewed>\nChanges: <specific changes made>\nOpen Questions: <any or —>'`
	case "qa":
		instruction = `In 2-3 sentences: what did the QA agent validate, what issues were found, and what was changed? List any open questions remaining. Format: 'Summary: <what was tested>\nChanges: <any fixes made>\nOpen Questions: <any or —>'`
	case "contract-check":
		instruction = `In 2-3 sentences: what contract violations or gaps were found, what was fixed, and what open questions (if any) remain for the next implementer. Format: 'Summary: <what was checked>\nChanges: <violations fixed>\nOpen Questions: <any or —>'`
	default:
		return "" // verify, merge: use static messages
	}

	return fmt.Sprintf(`%s

Session log (truncated to last 4000 chars):
%s

Git diff:
%s`, instruction, truncate(sessionLog, 4000), truncate(gitDiff, 2000))
}

// SummaryResult holds the parsed output of a claude --print summary call.
type SummaryResult struct {
	Summary       string
	Changes       string
	OpenQuestions string
}

// GenerateSummary calls claude --print with the given prompt and parses the result.
// Returns a zero-value SummaryResult (not an error) if claude is unavailable.
func GenerateSummary(prompt string) SummaryResult {
	if prompt == "" {
		return SummaryResult{}
	}

	out, err := exec.Command("claude", "--print", prompt).Output()
	if err != nil {
		return SummaryResult{Summary: "(summary unavailable)"}
	}

	return parseSummaryOutput(string(out))
}

func parseSummaryOutput(output string) SummaryResult {
	result := SummaryResult{OpenQuestions: "—"}
	for _, line := range strings.Split(output, "\n") {
		if after, ok := strings.CutPrefix(line, "Summary: "); ok {
			result.Summary = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "Changes: "); ok {
			result.Changes = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "Open Questions: "); ok {
			result.OpenQuestions = strings.TrimSpace(after)
		}
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "..." + s[len(s)-max:]
}
