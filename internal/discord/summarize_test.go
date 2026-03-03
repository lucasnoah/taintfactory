package discord

import (
	"strings"
	"testing"
)

func TestBuildSummaryPrompt_Implement(t *testing.T) {
	prompt := BuildSummaryPrompt("implement", "session log content here", "diff content here")
	if !strings.Contains(prompt, "implemented") {
		t.Error("implement prompt should ask about what was implemented")
	}
	if !strings.Contains(prompt, "session log content here") {
		t.Error("prompt should include session log")
	}
	if !strings.Contains(prompt, "diff content here") {
		t.Error("prompt should include git diff")
	}
}

func TestBuildSummaryPrompt_Review(t *testing.T) {
	prompt := BuildSummaryPrompt("review", "log", "diff")
	if !strings.Contains(prompt, "flag") {
		t.Error("review prompt should ask about what was flagged")
	}
}

func TestBuildSummaryPrompt_QA(t *testing.T) {
	prompt := BuildSummaryPrompt("qa", "log", "diff")
	if !strings.Contains(prompt, "QA") && !strings.Contains(prompt, "qa") && !strings.Contains(prompt, "validate") && !strings.Contains(prompt, "test") {
		t.Error("qa prompt should mention validation/testing")
	}
}

func TestBuildSummaryPrompt_ContractCheck(t *testing.T) {
	prompt := BuildSummaryPrompt("contract-check", "log", "diff")
	if !strings.Contains(prompt, "contract") {
		t.Error("contract-check prompt should mention contracts")
	}
}

func TestBuildSummaryPrompt_Verify(t *testing.T) {
	prompt := BuildSummaryPrompt("verify", "log", "diff")
	if prompt != "" {
		t.Error("verify stage should return empty prompt (uses static message)")
	}
}

func TestBuildSummaryPrompt_Merge(t *testing.T) {
	prompt := BuildSummaryPrompt("merge", "log", "diff")
	if prompt != "" {
		t.Error("merge stage should return empty prompt (uses static message)")
	}
}

func TestParseSummaryOutput(t *testing.T) {
	output := "Summary: Added 3 tables\nChanges: ListItems added WHERE clause\nOpen Questions: None"
	result := parseSummaryOutput(output)
	if result.Summary != "Added 3 tables" {
		t.Errorf("Summary = %q, want %q", result.Summary, "Added 3 tables")
	}
	if result.Changes != "ListItems added WHERE clause" {
		t.Errorf("Changes = %q, want %q", result.Changes, "ListItems added WHERE clause")
	}
	if result.OpenQuestions != "None" {
		t.Errorf("OpenQuestions = %q, want %q", result.OpenQuestions, "None")
	}
}

func TestParseSummaryOutput_Defaults(t *testing.T) {
	// Missing fields default to empty/dash
	result := parseSummaryOutput("Summary: Only a summary here")
	if result.Summary != "Only a summary here" {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.OpenQuestions != "—" {
		t.Errorf("OpenQuestions = %q, want —", result.OpenQuestions)
	}
}
