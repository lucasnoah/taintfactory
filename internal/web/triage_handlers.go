package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/triage"
)

// TriageDetailData is the view model for the triage detail page.
type TriageDetailData struct {
	State             *triage.TriageState
	Slug              string
	StageOrder        []StageStatusItem // reuse existing type from handlers.go
	History           []TriageHistoryView
	IsActive          bool
	SessionDot        string
	TmuxCmd           string
	HasLiveStream     bool
	UpdatedAgo        string
	IssueURL          string
	ShouldAutoRefresh bool
}

// TriageHistoryView wraps a TriageStageHistoryEntry with derived fields.
type TriageHistoryView struct {
	Stage    string
	Outcome  string
	Summary  string
	Duration string
}

// handleTriageDetail renders the triage detail page for a single issue.
// slug is the repo slug (e.g. "owner-repo"), issueStr is the issue number as a string.
func (s *Server) handleTriageDetail(w http.ResponseWriter, r *http.Request, slug, issueStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue number", http.StatusBadRequest)
		return
	}

	store := s.triageStoreFor(slug)
	ts, err := store.Get(issue)
	if err != nil {
		http.Error(w, "triage state not found", http.StatusNotFound)
		return
	}

	// Build stage order from config (progress bar)
	var stageOrder []StageStatusItem
	if cfg := s.triageConfigFor(ts.RepoRoot); cfg != nil {
		completedStages := make(map[string]string) // stage -> duration
		for _, h := range ts.StageHistory {
			completedStages[h.Stage] = h.Duration
		}
		for _, stage := range cfg.Stages {
			status := "upcoming"
			duration := ""
			if dur, done := completedStages[stage.ID]; done {
				status = "done"
				duration = fmtDuration(dur)
			} else if stage.ID == ts.CurrentStage && ts.Status == "in_progress" {
				status = "active"
			}
			stageOrder = append(stageOrder, StageStatusItem{
				ID:       stage.ID,
				Status:   status,
				Duration: duration,
			})
		}
	}

	// Build history view
	history := make([]TriageHistoryView, len(ts.StageHistory))
	for i, h := range ts.StageHistory {
		history[i] = TriageHistoryView{
			Stage:    h.Stage,
			Outcome:  h.Outcome,
			Summary:  h.Summary,
			Duration: fmtDuration(h.Duration),
		}
	}

	// Tmux attach command
	var tmuxCmd string
	if ts.CurrentSession != "" {
		tmuxCmd = "tmux attach -t " + ts.CurrentSession
	}

	// Live stream available?
	hasLiveStream := false
	if ts.CurrentSession != "" {
		if _, err := capturePane(ts.CurrentSession); err == nil {
			hasLiveStream = true
		}
	}

	// GitHub issue URL
	var issueURL string
	if ts.Repo != "" {
		issueURL = fmt.Sprintf("https://github.com/%s/issues/%d", ts.Repo, issue)
	}

	data := TriageDetailData{
		State:             ts,
		Slug:              slug,
		StageOrder:        stageOrder,
		History:           history,
		IsActive:          ts.Status == "in_progress",
		SessionDot:        s.sessionDot(ts.CurrentSession),
		TmuxCmd:           tmuxCmd,
		HasLiveStream:     hasLiveStream,
		UpdatedAgo:        relTime(ts.UpdatedAt),
		IssueURL:          issueURL,
		ShouldAutoRefresh: ts.Status == "in_progress" && !hasLiveStream,
	}

	if err := s.triageTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleTriageStream streams the tmux session output for a triage issue via SSE.
func (s *Server) handleTriageStream(w http.ResponseWriter, r *http.Request, slug, issueStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendDone := func(reason string) {
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", reason)
		flusher.Flush()
	}

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}

		store := s.triageStoreFor(slug)
		ts, err := store.Get(issue)
		if err != nil {
			sendDone("triage state not found")
			return
		}
		if ts.CurrentSession == "" {
			sendDone("no active session")
			return
		}

		output, err := capturePane(ts.CurrentSession)
		if err != nil {
			sendDone("session ended")
			return
		}

		for _, line := range strings.Split(output, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}
}
