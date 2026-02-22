package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/prompt"
)

// ---- view models ----

type DashboardData struct {
	Pipelines      []PipelineRow
	QueueItems     []QueueRowView
	RecentActivity []ActivityRow
}

type PipelineRow struct {
	Issue        int
	Title        string
	Status       string
	CurrentStage string
	UpdatedAgo   string
	SessionDot   string
	IsLive       bool // true when tmux session is confirmed alive right now
}

type QueueRowView struct {
	Issue          int
	Position       int
	Status         string
	DependsOnStr   string
	HasPipeline    bool
	PipelineStatus string
}

type ActivityRow struct {
	Issue   int
	Event   string
	Stage   string
	TimeAgo string
}

type PipelineDetailData struct {
	State         *pipeline.PipelineState
	StageOrder    []StageStatusItem
	History       []StageHistoryView
	Events        []db.PipelineEvent
	IsActive      bool
	SessionDot    string
	TmuxCmd       string // tmux attach command for the current active session
	HasLiveStream bool   // true when session is active and tmux is available
	UpdatedAgo    string
	IssueURL      string // fully-qualified GitHub issue URL, empty if repo not configured
}

type StageStatusItem struct {
	ID       string
	Status   string // "done", "active", "upcoming"
	Duration string // human-readable duration for completed stages, empty otherwise
}

// StageHistoryView wraps a StageHistoryEntry with a pre-computed tmux attach command.
type StageHistoryView struct {
	pipeline.StageHistoryEntry
	TmuxCmd string
}

type AttemptDetailData struct {
	Issue        int
	Stage        string
	Attempt      int
	Prompt       string
	Log          string
	LogTruncated bool
	Checks       []db.CheckRun
	Summary      *pipeline.StageSummary
	Outcome      *pipeline.StageOutcome
}

type QueueData struct {
	Items []QueueRowView
}

type ConfigData struct {
	Repos []RepoConfigView
}

type RepoConfigView struct {
	Dir        string
	ConfigName string
	Stages     []StageConfigRow
	Checks     []CheckConfigRow
}

type StageConfigRow struct {
	ID             string
	Type           string
	Model          string
	GoalGate       bool
	ChecksAfterStr string
	PromptTemplate string // filename, e.g. "implement.md"
	PromptContent  string // loaded text; empty if not an agent stage or template not found
}

type CheckConfigRow struct {
	Name    string
	Command string
	Parser  string
	AutoFix bool
	Timeout string
}

// ---- helpers ----

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07]*\x07|\x1b[()][012B]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func relTime(ts string) string {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	}
	var t time.Time
	for _, f := range formats {
		if parsed, err := time.Parse(f, ts); err == nil {
			t = parsed
			break
		}
	}
	if t.IsZero() {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func fmtDuration(s string) string {
	d, err := time.ParseDuration(s)
	if err != nil || d == 0 {
		return ""
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func formatDeps(deps []int) string {
	if len(deps) == 0 {
		return ""
	}
	parts := make([]string, len(deps))
	for i, d := range deps {
		parts[i] = fmt.Sprintf("#%d", d)
	}
	return strings.Join(parts, ", ")
}

func (s *Server) sessionDot(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	ev, err := s.db.GetSessionState(sessionID)
	if err != nil || ev == nil {
		return ""
	}
	return sessionEventLabel(ev.Event)
}

func (s *Server) execTemplate(w http.ResponseWriter, tmpl interface {
	ExecuteTemplate(http.ResponseWriter, string, interface{}) error
}, data interface{}) {
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Dashboard ----

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	pipelines, err := s.store.List("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	queueItems, err := s.db.QueueList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	activity, _ := s.recentActivity(20)

	pipelineByIssue := make(map[int]*pipeline.PipelineState)
	for i := range pipelines {
		p := &pipelines[i]
		pipelineByIssue[p.Issue] = p
	}

	rows := make([]PipelineRow, 0, len(pipelines))
	for _, p := range pipelines {
		isLive := false
		if p.Status == "in_progress" && p.CurrentSession != "" {
			if _, err := capturePane(p.CurrentSession); err == nil {
				isLive = true
			}
		}
		rows = append(rows, PipelineRow{
			Issue:        p.Issue,
			Title:        p.Title,
			Status:       p.Status,
			CurrentStage: p.CurrentStage,
			UpdatedAgo:   relTime(p.UpdatedAt),
			SessionDot:   s.sessionDot(p.CurrentSession),
			IsLive:       isLive,
		})
	}

	queueRows := make([]QueueRowView, 0, len(queueItems))
	for _, q := range queueItems {
		var pStatus string
		hasPipeline := false
		if p, ok := pipelineByIssue[q.Issue]; ok {
			hasPipeline = true
			pStatus = p.Status
		}
		queueRows = append(queueRows, QueueRowView{
			Issue:          q.Issue,
			Position:       q.Position,
			Status:         q.Status,
			DependsOnStr:   formatDeps(q.DependsOn),
			HasPipeline:    hasPipeline,
			PipelineStatus: pStatus,
		})
	}

	activityRows := make([]ActivityRow, 0, len(activity))
	for _, e := range activity {
		activityRows = append(activityRows, ActivityRow{
			Issue:   e.Issue,
			Event:   e.Event,
			Stage:   e.Stage,
			TimeAgo: relTime(e.Timestamp),
		})
	}

	// Reverse both lists so newest/highest-position items appear first.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	for i, j := 0, len(queueRows)-1; i < j; i, j = i+1, j-1 {
		queueRows[i], queueRows[j] = queueRows[j], queueRows[i]
	}

	data := DashboardData{
		Pipelines:      rows,
		QueueItems:     queueRows,
		RecentActivity: activityRows,
	}

	if err := s.dashboardTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Pipeline Detail ----

func (s *Server) handlePipelineDetail(w http.ResponseWriter, r *http.Request, issueStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue number", http.StatusBadRequest)
		return
	}

	ps, err := s.store.Get(issue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	events, _ := s.db.GetPipelineHistory(issue)

	var stageOrder []StageStatusItem
	if cfg := s.configFor(ps.Worktree); cfg != nil {
		succeeded := make(map[string]bool)
		stageDuration := make(map[string]string)
		for _, h := range ps.StageHistory {
			if h.Outcome == "success" {
				succeeded[h.Stage] = true
				stageDuration[h.Stage] = fmtDuration(h.Duration)
			}
		}
		for _, stage := range cfg.Pipeline.Stages {
			status := "upcoming"
			if succeeded[stage.ID] {
				status = "done"
			} else if stage.ID == ps.CurrentStage {
				status = "active"
			}
			stageOrder = append(stageOrder, StageStatusItem{
				ID:       stage.ID,
				Status:   status,
				Duration: stageDuration[stage.ID],
			})
		}
	}

	history := make([]StageHistoryView, len(ps.StageHistory))
	for i, h := range ps.StageHistory {
		sessionName := fmt.Sprintf("%d-%s-%d", issue, h.Stage, h.Attempt)
		history[i] = StageHistoryView{
			StageHistoryEntry: h,
			TmuxCmd:           "tmux attach -t " + sessionName,
		}
	}

	var tmuxCmd string
	if ps.CurrentSession != "" {
		tmuxCmd = "tmux attach -t " + ps.CurrentSession
	} else if ps.Status == "in_progress" {
		// Derive from current stage/attempt even if session field was cleared
		tmuxCmd = "tmux attach -t " + fmt.Sprintf("%d-%s-%d", issue, ps.CurrentStage, ps.CurrentAttempt)
	}

	// Live stream is available when the session is active in tmux right now.
	hasLiveStream := false
	if ps.CurrentSession != "" {
		if _, err := capturePane(ps.CurrentSession); err == nil {
			hasLiveStream = true
		}
	}

	var issueURL string
	if cfg := s.configFor(ps.Worktree); cfg != nil && cfg.Pipeline.Repo != "" {
		repo := strings.TrimPrefix(cfg.Pipeline.Repo, "github.com/")
		issueURL = fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue)
	}

	data := PipelineDetailData{
		State:         ps,
		StageOrder:    stageOrder,
		History:       history,
		Events:        events,
		IsActive:      ps.Status == "in_progress",
		SessionDot:    s.sessionDot(ps.CurrentSession),
		TmuxCmd:       tmuxCmd,
		HasLiveStream: hasLiveStream,
		UpdatedAgo:    relTime(ps.UpdatedAt),
		IssueURL:      issueURL,
	}

	if err := s.pipelineTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Attempt Detail ----

func (s *Server) handleAttemptDetail(w http.ResponseWriter, r *http.Request, issueStr, stage, attemptStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue number", http.StatusBadRequest)
		return
	}
	attempt, err := strconv.Atoi(attemptStr)
	if err != nil {
		http.Error(w, "invalid attempt number", http.StatusBadRequest)
		return
	}

	prompt, _ := s.store.GetPrompt(issue, stage, attempt)
	logContent, _ := s.store.GetSessionLog(issue, stage, attempt)
	checks, _ := s.checkRunsForAttempt(issue, stage, attempt)
	summary, _ := s.store.GetStageSummary(issue, stage, attempt)
	outcome, _ := s.store.GetStageOutcome(issue, stage, attempt)

	logContent = stripANSI(logContent)
	const logLineLimit = 200
	logLines := strings.Split(logContent, "\n")
	logTruncated := false
	if len(logLines) > logLineLimit {
		logLines = logLines[len(logLines)-logLineLimit:]
		logTruncated = true
	}

	data := AttemptDetailData{
		Issue:        issue,
		Stage:        stage,
		Attempt:      attempt,
		Prompt:       prompt,
		Log:          strings.Join(logLines, "\n"),
		LogTruncated: logTruncated,
		Checks:       checks,
		Summary:      summary,
		Outcome:      outcome,
	}

	if err := s.attemptTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Attempt Log (raw text/plain) ----

func (s *Server) handleAttemptLog(w http.ResponseWriter, r *http.Request, issueStr, stage, attemptStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue number", http.StatusBadRequest)
		return
	}
	attempt, err := strconv.Atoi(attemptStr)
	if err != nil {
		http.Error(w, "invalid attempt number", http.StatusBadRequest)
		return
	}

	logContent, err := s.store.GetSessionLog(issue, stage, attempt)
	if err != nil {
		http.Error(w, "log not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, stripANSI(logContent))
}

// ---- Queue ----

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	queueItems, err := s.db.QueueList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pipelines, _ := s.store.List("")
	pipelineByIssue := make(map[int]*pipeline.PipelineState)
	for i := range pipelines {
		p := &pipelines[i]
		pipelineByIssue[p.Issue] = p
	}

	rows := make([]QueueRowView, 0, len(queueItems))
	for _, q := range queueItems {
		var pStatus string
		hasPipeline := false
		if p, ok := pipelineByIssue[q.Issue]; ok {
			hasPipeline = true
			pStatus = p.Status
		}
		rows = append(rows, QueueRowView{
			Issue:          q.Issue,
			Position:       q.Position,
			Status:         q.Status,
			DependsOnStr:   formatDeps(q.DependsOn),
			HasPipeline:    hasPipeline,
			PipelineStatus: pStatus,
		})
	}

	data := QueueData{Items: rows}
	if err := s.queueTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Config ----

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	repos := s.allRepoConfigs()

	var views []RepoConfigView
	for _, rc := range repos {
		view := RepoConfigView{
			Dir:        rc.Dir,
			ConfigName: rc.Cfg.Pipeline.Name,
		}
		for _, stage := range rc.Cfg.Pipeline.Stages {
			stageType := stage.Type
			if stageType == "" {
				stageType = "agent"
			}
			// Resolve template name: explicit field, else {stage.ID}.md by convention.
			tmplName := stage.PromptTemplate
			if tmplName == "" && stageType == "agent" {
				tmplName = stage.ID + ".md"
			}
			var promptContent string
			if tmplName != "" {
				if content, err := prompt.LoadTemplate(tmplName, rc.Dir); err == nil {
					promptContent = content
				}
			}
			view.Stages = append(view.Stages, StageConfigRow{
				ID:             stage.ID,
				Type:           stageType,
				Model:          stage.Model,
				GoalGate:       stage.GoalGate,
				ChecksAfterStr: strings.Join(stage.ChecksAfter, ", "),
				PromptTemplate: tmplName,
				PromptContent:  promptContent,
			})
		}
		for name, check := range rc.Cfg.Pipeline.Checks {
			view.Checks = append(view.Checks, CheckConfigRow{
				Name:    name,
				Command: check.Command,
				Parser:  check.Parser,
				AutoFix: check.AutoFix,
				Timeout: check.Timeout,
			})
		}
		views = append(views, view)
	}

	data := ConfigData{Repos: views}
	if err := s.configTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
