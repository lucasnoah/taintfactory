package triage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestAdvanceLock_ExclusiveAccess verifies that acquiring the lock a second time
// returns an error while the first holder has it.
func TestAdvanceLock_ExclusiveAccess(t *testing.T) {
	dir := t.TempDir()

	release, err := acquireAdvanceLock(dir)
	if err != nil {
		t.Fatalf("first acquireAdvanceLock: %v", err)
	}
	defer release()

	// Second acquisition must fail.
	_, err2 := acquireAdvanceLock(dir)
	if err2 == nil {
		t.Error("second acquireAdvanceLock: expected error (lock held), got nil")
	}
}

// TestAdvanceLock_ReleasedAllowsReacquire verifies that after release the lock
// can be acquired again.
func TestAdvanceLock_ReleasedAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	release, err := acquireAdvanceLock(dir)
	if err != nil {
		t.Fatalf("first acquireAdvanceLock: %v", err)
	}
	release() // explicitly release

	release2, err := acquireAdvanceLock(dir)
	if err != nil {
		t.Errorf("re-acquire after release: unexpected error: %v", err)
	} else {
		release2()
	}
}

// TestAdvanceLock_StaleLockIsIgnored verifies that a lock file older than 30
// minutes is treated as stale and overwritten.
func TestAdvanceLock_StaleLockIsIgnored(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".advance.lock")

	// Create a "stale" lock file with a modification time 31 minutes ago.
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	staleTime := time.Now().Add(-31 * time.Minute)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatalf("set mtime: %v", err)
	}

	release, err := acquireAdvanceLock(dir)
	if err != nil {
		t.Errorf("acquireAdvanceLock over stale lock: unexpected error: %v", err)
	} else {
		release()
	}
}

// TestRunner_Advance_SkipsWhenLockHeld verifies that Advance() returns no
// actions (and no error) when another process holds the advance lock.
func TestRunner_Advance_SkipsWhenLockHeld(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	executed := false
	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		executed = true
		return `{"outcome":"yes","summary":"should not run"}`, nil
	}

	st := &TriageState{
		Issue:        31,
		Repo:         "owner/test",
		CurrentStage: "classifier_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate another process holding the lock.
	release, err := acquireAdvanceLock(store.BaseDir())
	if err != nil {
		t.Fatalf("acquireAdvanceLock: %v", err)
	}
	defer release()

	actions, err := runner.Advance()
	if err != nil {
		t.Errorf("Advance() error = %v, want nil", err)
	}
	if len(actions) != 0 {
		t.Errorf("Advance() returned %d actions, want 0 (lock was held)", len(actions))
	}
	if executed {
		t.Error("printExec was called despite lock being held")
	}

	got, err := store.Get(31)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want pending (should not have changed while locked)", got.Status)
	}
}

// TestRunner_Advance_NoConcurrentDuplicates verifies that two concurrent Advance()
// calls do not produce duplicate stage_history entries.
func TestRunner_Advance_NoConcurrentDuplicates(t *testing.T) {
	cfg := testChainedPrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")
	writePrintStageTemplate(t, repoRoot, "classifier_b")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	// Barrier ensures both goroutines are inside printExec for classifier_a at the
	// same time — maximising the chance of overlap in the critical section.
	var (
		mu           sync.Mutex
		firstReached = false
	)
	gate := make(chan struct{})

	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		if stageCfg.ID == "classifier_a" {
			mu.Lock()
			if !firstReached {
				firstReached = true
				mu.Unlock()
				// First goroutine: wait for the second to arrive.
				select {
				case <-gate:
				case <-time.After(200 * time.Millisecond):
					// Timeout: second goroutine never arrived (it skipped due to lock). OK.
				}
			} else {
				mu.Unlock()
				// Second goroutine: signal the first and proceed.
				select {
				case gate <- struct{}{}:
				default:
				}
			}
		}
		return `{"outcome":"yes","summary":"concurrent test"}`, nil
	}

	st := &TriageState{
		Issue:        32,
		Repo:         "owner/test",
		CurrentStage: "classifier_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runner.Advance() //nolint:errcheck
	}()
	go func() {
		defer wg.Done()
		runner.Advance() //nolint:errcheck
	}()
	wg.Wait()

	got, err := store.Get(32)
	if err != nil {
		t.Fatalf("Get after concurrent Advance: %v", err)
	}
	if len(got.StageHistory) != 2 {
		t.Errorf("StageHistory has %d entries, want exactly 2 (concurrent Advance caused duplicates)", len(got.StageHistory))
	}
}
