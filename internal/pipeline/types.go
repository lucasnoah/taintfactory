package pipeline

// PipelineState is the top-level persisted state for a single issue pipeline.
type PipelineState struct {
	Issue          int                 `json:"issue"`
	Title          string              `json:"title"`
	Branch         string              `json:"branch"`
	Worktree       string              `json:"worktree"`
	CurrentStage   string              `json:"current_stage"`
	CurrentAttempt int                 `json:"current_attempt"`
	CurrentSession string              `json:"current_session"`
	CurrentFixRound int               `json:"current_fix_round"`
	StageHistory   []StageHistoryEntry `json:"stage_history"`
	GoalGates      map[string]string   `json:"goal_gates"`
	Status         string              `json:"status"` // "pending", "in_progress", "completed", "failed", "blocked"
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}

// StageHistoryEntry records the outcome of a completed stage attempt.
type StageHistoryEntry struct {
	Stage          string `json:"stage"`
	Attempt        int    `json:"attempt"`
	Outcome        string `json:"outcome"`
	Duration       string `json:"duration"`
	FixRounds      int    `json:"fix_rounds"`
	ChecksFirstPass bool  `json:"checks_first_pass"`
}

// StageOutcome captures the result of running a stage.
type StageOutcome struct {
	Status         string            `json:"status"` // "success", "fail", "escalate"
	Summary        string            `json:"summary"`
	FilesChanged   []string          `json:"files_changed,omitempty"`
	DiffSummary    string            `json:"diff_summary,omitempty"`
	Findings       []Finding         `json:"findings,omitempty"`
	ContextUpdates map[string]string `json:"context_updates,omitempty"`
}

// Finding represents a single lint/test/check finding.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Rule     string `json:"rule,omitempty"`
}

// StageSummary is the final summary of a stage attempt including fix-loop stats.
type StageSummary struct {
	Stage           string            `json:"stage"`
	Attempt         int               `json:"attempt"`
	Outcome         string            `json:"outcome"`
	AgentDuration   string            `json:"agent_duration"`
	TotalDuration   string            `json:"total_duration"`
	FixRounds       int               `json:"fix_rounds"`
	ChecksFirstPass bool              `json:"checks_first_pass"`
	AutoFixes       map[string]int    `json:"auto_fixes"`
	AgentFixes      map[string]int    `json:"agent_fixes"`
	FinalCheckState map[string]string `json:"final_check_state"`
}
