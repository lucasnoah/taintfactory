package discord

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAgentEmbed_Success(t *testing.T) {
	params := EmbedParams{
		Issue:         285,
		Namespace:     "mbrucker/deathcookies",
		Stage:         "implement",
		Outcome:       "success",
		Duration:      "10m32s",
		FixRounds:     0,
		StageIndex:    1,
		TotalStages:   6,
		Summary:       "Added pl_accounts, pl_targets tables with 9 sqlc queries.",
		Changes:       "ListPLAccounts: added WHERE active=true filter.",
		OpenQuestions: "",
	}

	payload := BuildAgentEmbed(params)

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, "285") {
		t.Error("expected issue number in embed")
	}
	if !strings.Contains(s, "implement") {
		t.Error("expected stage name in embed")
	}
	// Green color for success with no fix rounds
	if !strings.Contains(s, "3066993") { // 0x2ECC71
		t.Error("expected green color for success")
	}
}

func TestBuildAgentEmbed_FixRoundsYellow(t *testing.T) {
	params := EmbedParams{
		Issue:     285,
		Namespace: "ns/repo",
		Stage:     "implement",
		Outcome:   "success",
		FixRounds: 2,
	}
	payload := BuildAgentEmbed(params)
	data, _ := json.Marshal(payload)
	s := string(data)
	// Yellow color for success with fix rounds
	if !strings.Contains(s, "16312092") { // 0xF8C419
		t.Error("expected yellow color for success+fixrounds")
	}
}

func TestBuildAgentEmbed_FailColor(t *testing.T) {
	params := EmbedParams{
		Issue:     285,
		Namespace: "ns/repo",
		Stage:     "review",
		Outcome:   "fail",
		Duration:  "5m",
		FixRounds: 2,
	}
	payload := BuildAgentEmbed(params)
	data, _ := json.Marshal(payload)
	s := string(data)
	// Red color for failure
	if !strings.Contains(s, "15158332") { // 0xE74C3C
		t.Error("expected red color for failure")
	}
}

func TestBuildCompletionEmbed(t *testing.T) {
	params := CompletionEmbedParams{
		Issue:         285,
		Namespace:     "mbrucker/deathcookies",
		Outcome:       "completed",
		TotalDuration: "49m3s",
		IssueTitle:    "P&L DB layer: schema + sqlc queries",
		StageChain:    "implement → review → qa → verify → merge → contract-check",
	}
	payload := BuildCompletionEmbed(params)
	data, _ := json.Marshal(payload)
	s := string(data)
	if !strings.Contains(s, "49m3s") {
		t.Error("expected total duration in embed")
	}
	if !strings.Contains(s, "DB layer") {
		t.Error("expected issue title in embed footer")
	}
}
