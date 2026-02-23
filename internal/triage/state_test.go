package triage

import (
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
	s := NewStore("/tmp/triage-test-base")

	got := s.OutcomePath(7, "stale_context")
	want := "/tmp/triage-test-base/7/stale_context.outcome.json"
	if got != want {
		t.Errorf("OutcomePath(7, \"stale_context\") = %q, want %q", got, want)
	}
}
