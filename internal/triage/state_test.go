package triage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func TestStore_SaveAndGet(t *testing.T) {
	s := newTestStore(t)

	state := &TriageState{
		Issue:        42,
		Repo:         "owner/repo",
		CurrentStage: "stale_context",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}

	if err := s.Save(state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got, err := s.Get(42)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if got.Issue != 42 {
		t.Errorf("Issue = %d, want 42", got.Issue)
	}
	if got.CurrentStage != "stale_context" {
		t.Errorf("CurrentStage = %q, want %q", got.CurrentStage, "stale_context")
	}
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}
	if got.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want %q", got.Repo, "owner/repo")
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Get(999)
	if err == nil {
		t.Error("Get() expected error for missing issue, got nil")
	}
}

func TestStore_List(t *testing.T) {
	s := newTestStore(t)

	states := []*TriageState{
		{Issue: 1, Repo: "owner/repo", CurrentStage: "stage_a", Status: "in_progress", StageHistory: []TriageStageHistoryEntry{}},
		{Issue: 2, Repo: "owner/repo", CurrentStage: "stage_b", Status: "in_progress", StageHistory: []TriageStageHistoryEntry{}},
		{Issue: 3, Repo: "owner/repo", CurrentStage: "stage_c", Status: "completed", StageHistory: []TriageStageHistoryEntry{}},
	}

	for _, st := range states {
		if err := s.Save(st); err != nil {
			t.Fatalf("Save(issue=%d) error: %v", st.Issue, err)
		}
	}

	all, err := s.List("")
	if err != nil {
		t.Fatalf("List(\"\") error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List(\"\") returned %d items, want 3", len(all))
	}

	inProgress, err := s.List("in_progress")
	if err != nil {
		t.Fatalf("List(\"in_progress\") error: %v", err)
	}
	if len(inProgress) != 2 {
		t.Errorf("List(\"in_progress\") returned %d items, want 2", len(inProgress))
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)

	before := time.Now().UTC().Truncate(time.Second)

	state := &TriageState{
		Issue:        10,
		Repo:         "owner/repo",
		CurrentStage: "stage_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := s.Save(state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if err := s.Update(10, func(st *TriageState) {
		st.Status = "in_progress"
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	got, err := s.Get(10)
	if err != nil {
		t.Fatalf("Get() after Update() error: %v", err)
	}

	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}

	if got.UpdatedAt == "" {
		t.Error("UpdatedAt is empty after Update()")
	}

	updatedAt, err := time.Parse(time.RFC3339, got.UpdatedAt)
	if err != nil {
		t.Fatalf("parsing UpdatedAt %q: %v", got.UpdatedAt, err)
	}
	if updatedAt.Before(before) {
		t.Errorf("UpdatedAt %v is before test start %v", updatedAt, before)
	}
}

func TestStore_OutcomePath(t *testing.T) {
	s := NewStore(t.TempDir())

	got := s.OutcomePath(7, "stale_context")
	// The path must end with /<issue>/<stageID>.outcome.json
	wantSuffix := "/7/stale_context.outcome.json"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("OutcomePath(7, \"stale_context\") = %q, want suffix %q", got, wantSuffix)
	}
}

func TestStore_EnsureOutcomeDir(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.EnsureOutcomeDir(42); err != nil {
		t.Fatalf("EnsureOutcomeDir() error: %v", err)
	}
	// Calling again should be idempotent
	if err := store.EnsureOutcomeDir(42); err != nil {
		t.Fatalf("EnsureOutcomeDir() second call error: %v", err)
	}
	// Directory should exist
	dir := filepath.Dir(store.OutcomePath(42, "test"))
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("outcome dir not created: %v", err)
	}
}

func TestStore_ReadOutcome(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.EnsureOutcomeDir(42); err != nil {
		t.Fatal(err)
	}
	// Write an outcome file manually
	data := []byte(`{"outcome":"clean","summary":"All good"}`)
	if err := os.WriteFile(store.OutcomePath(42, "stale_context"), data, 0644); err != nil {
		t.Fatal(err)
	}
	outcome, err := store.ReadOutcome(42, "stale_context")
	if err != nil {
		t.Fatalf("ReadOutcome() error: %v", err)
	}
	if outcome.Outcome != "clean" {
		t.Errorf("Outcome = %q, want clean", outcome.Outcome)
	}
	if outcome.Summary != "All good" {
		t.Errorf("Summary = %q, want 'All good'", outcome.Summary)
	}
}

func TestStore_ReadOutcome_NotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.ReadOutcome(42, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing outcome file, got nil")
	}
}
