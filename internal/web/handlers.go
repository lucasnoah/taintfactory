package web

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
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
	TriageRows     []TriageRow
	ProjectSummary []ProjectSummaryCard
	Sidebar        SidebarData
}

type TriageListData struct {
	TriageRows []TriageRow
	Sidebar    SidebarData
}

type TriageRow struct {
	Slug         string // filesystem slug for URL (e.g. "mbrucker-deathcookies")
	Issue        int
	Repo         string
	Status       string
	CurrentStage string
	UpdatedAgo   string
	SessionDot   string
	IsLive       bool
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

type ProjectSidebarItem struct {
	Namespace   string
	ActiveCount int
	IsSelected  bool
}

type SidebarData struct {
	Projects       []ProjectSidebarItem
	CurrentProject string // empty = All view
}

type ProjectSummaryCard struct {
	Namespace   string
	ActiveCount int
	TotalCount  int
	FailedCount int
}

type QueueRowView struct {
	Issue          int
	Position       int
	Status         string
	DependsOnStr   string
	HasPipeline    bool
	PipelineStatus string
	Namespace      string // project namespace; empty for legacy
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
	QueueStatus      string // queue status for this issue ("pending","active","completed","" if not queued)
	Upstream         []DepIssueView // issues this one depends on (must complete first)
	Downstream       []DepIssueView // issues that depend on this one (blocked until this completes)
	ShouldAutoRefresh bool // true when active but no live SSE stream (meta-refresh fallback)
	Sidebar          SidebarData
}

// DepIssueView represents a dependency relationship to another issue.
type DepIssueView struct {
	Issue          int
	Title          string
	QueueStatus    string
	PipelineStatus string
	HasPipeline    bool
	GithubURL      string // fully-qualified GitHub issue URL, empty if repo not configured
}

type StageStatusItem struct {
	ID       string
	Status   string // "done", "active", "upcoming"
	Duration string // human-readable duration for completed stages, empty otherwise
	Model    string // resolved model for this stage
}

// StageHistoryView wraps a StageHistoryEntry with a pre-computed tmux attach command.
type StageHistoryView struct {
	pipeline.StageHistoryEntry
	TmuxCmd string
	Model   string
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
	Sidebar      SidebarData
}

type QueueData struct {
	Items   []QueueRowView
	Sidebar SidebarData
}

type ConfigData struct {
	Repos   []RepoConfigView
	Sidebar SidebarData
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
	proj := currentProject(r)
	sidebar := s.sidebarData(proj)

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

	// Sort pipelines by updated_at descending (most recently active first).
	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].UpdatedAt > pipelines[j].UpdatedAt
	})

	// Build project summary cards before filtering (needs all namespaced pipelines).
	var projectSummary []ProjectSummaryCard
	if proj == "" {
		nsCounts := make(map[string]*ProjectSummaryCard)
		for _, p := range pipelines {
			if p.Namespace == "" {
				continue
			}
			c := nsCounts[p.Namespace]
			if c == nil {
				c = &ProjectSummaryCard{Namespace: p.Namespace}
				nsCounts[p.Namespace] = c
			}
			c.TotalCount++
			if p.Status == "in_progress" {
				c.ActiveCount++
			}
			if p.Status == "failed" {
				c.FailedCount++
			}
		}
		for _, c := range nsCounts {
			projectSummary = append(projectSummary, *c)
		}
		sort.Slice(projectSummary, func(i, j int) bool {
			return projectSummary[i].Namespace < projectSummary[j].Namespace
		})
	}

	// Filter pipelines by project.
	if proj != "" {
		var filtered []pipeline.PipelineState
		for _, p := range pipelines {
			if p.Namespace == proj {
				filtered = append(filtered, p)
			}
		}
		pipelines = filtered
	}

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

	// Build queue rows, filtered by project and annotated with namespace.
	queueRows := make([]QueueRowView, 0, len(queueItems))
	for _, q := range queueItems {
		ns := ""
		if p, ok := pipelineByIssue[q.Issue]; ok {
			ns = p.Namespace
		} else {
			ns = s.namespaceFromConfigPath(q.ConfigPath)
		}
		if proj != "" && ns != proj {
			continue
		}
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
			Namespace:      ns,
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

	// Reverse queue so highest-position items appear first.
	for i, j := 0, len(queueRows)-1; i < j; i, j = i+1, j-1 {
		queueRows[i], queueRows[j] = queueRows[j], queueRows[i]
	}

	// Collect active/recent triage states (in_progress first, then recently completed).
	triageStates := s.allTriageStates()
	sort.Slice(triageStates, func(i, j int) bool {
		// in_progress before completed; within same status, most-recently-updated first
		si, sj := triageStates[i].Status, triageStates[j].Status
		if si != sj {
			if si == "in_progress" {
				return true
			}
			if sj == "in_progress" {
				return false
			}
		}
		return triageStates[i].UpdatedAt > triageStates[j].UpdatedAt
	})
	triageRows := make([]TriageRow, 0, len(triageStates))
	for _, ts := range triageStates {
		isLive := false
		if ts.Status == "in_progress" && ts.CurrentSession != "" {
			if _, err := capturePane(ts.CurrentSession); err == nil {
				isLive = true
			}
		}
		slug := strings.ReplaceAll(ts.Repo, "/", "-")
		triageRows = append(triageRows, TriageRow{
			Slug:         slug,
			Issue:        ts.Issue,
			Repo:         ts.Repo,
			Status:       ts.Status,
			CurrentStage: ts.CurrentStage,
			UpdatedAgo:   relTime(ts.UpdatedAt),
			SessionDot:   s.sessionDot(ts.CurrentSession),
			IsLive:       isLive,
		})
	}

	data := DashboardData{
		Pipelines:      rows,
		QueueItems:     queueRows,
		RecentActivity: activityRows,
		TriageRows:     triageRows,
		ProjectSummary: projectSummary,
		Sidebar:        sidebar,
	}

	if err := s.dashboardTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Triage List ----

func (s *Server) handleTriageList(w http.ResponseWriter, r *http.Request) {
	triageStates := s.allTriageStates()
	sort.Slice(triageStates, func(i, j int) bool {
		return triageStates[i].UpdatedAt > triageStates[j].UpdatedAt
	})
	rows := make([]TriageRow, 0, len(triageStates))
	for _, ts := range triageStates {
		isLive := false
		if ts.Status == "in_progress" && ts.CurrentSession != "" {
			if _, err := capturePane(ts.CurrentSession); err == nil {
				isLive = true
			}
		}
		// slug must match the on-disk triage directory name under triageDir
		slug := strings.ReplaceAll(ts.Repo, "/", "-")
		rows = append(rows, TriageRow{
			Slug:         slug,
			Issue:        ts.Issue,
			Repo:         ts.Repo,
			Status:       ts.Status,
			CurrentStage: ts.CurrentStage,
			UpdatedAgo:   relTime(ts.UpdatedAt),
			SessionDot:   s.sessionDot(ts.CurrentSession),
			IsLive:       isLive,
		})
	}
	data := TriageListData{TriageRows: rows, Sidebar: s.sidebarData("")}
	if err := s.triageListTmpl.ExecuteTemplate(w, "base", data); err != nil {
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
	if cfg := s.configForPS(ps); cfg != nil {
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
			var model string
			stageType := stage.Type
			if stageType == "" {
				stageType = "agent"
			}
			if stageType == "agent" {
				model = stage.Model
				if model == "" {
					model = cfg.Pipeline.Defaults.Model
				}
				if model == "" {
					model = "claude-opus-4-6"
				}
			}
			stageOrder = append(stageOrder, StageStatusItem{
				ID:       stage.ID,
				Status:   status,
				Duration: stageDuration[stage.ID],
				Model:    model,
			})
		}
	}

	// Build a model lookup from config for history rows (agent stages only).
	stageModel := make(map[string]string)
	if cfg := s.configForPS(ps); cfg != nil {
		for _, stage := range cfg.Pipeline.Stages {
			t := stage.Type
			if t == "" {
				t = "agent"
			}
			if t != "agent" {
				continue
			}
			model := stage.Model
			if model == "" {
				model = cfg.Pipeline.Defaults.Model
			}
			if model == "" {
				model = "claude-opus-4-6"
			}
			stageModel[stage.ID] = model
		}
	}

	history := make([]StageHistoryView, len(ps.StageHistory))
	for i, h := range ps.StageHistory {
		sessionName := fmt.Sprintf("%d-%s-%d", issue, h.Stage, h.Attempt)
		history[i] = StageHistoryView{
			StageHistoryEntry: h,
			TmuxCmd:           "tmux attach -t " + sessionName,
			Model:             stageModel[h.Stage],
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
	if cfg := s.configForPS(ps); cfg != nil && cfg.Pipeline.Repo != "" {
		repo := strings.TrimPrefix(cfg.Pipeline.Repo, "github.com/")
		issueURL = fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue)
	}

	// Build dependency graph from queue.
	var repoBase string // e.g. "https://github.com/owner/repo/issues/"
	if cfg := s.configForPS(ps); cfg != nil && cfg.Pipeline.Repo != "" {
		repo := strings.TrimPrefix(cfg.Pipeline.Repo, "github.com/")
		repoBase = fmt.Sprintf("https://github.com/%s/issues/", repo)
	}

	var queueStatus string
	var upstream, downstream []DepIssueView
	if allQueue, err := s.db.QueueList(); err == nil {
		qByIssue := make(map[int]db.QueueItem, len(allQueue))
		for _, q := range allQueue {
			qByIssue[q.Issue] = q
		}
		if q, ok := qByIssue[issue]; ok {
			queueStatus = q.Status
			for _, depIssue := range q.DependsOn {
				dv := depIssueView(depIssue, qByIssue, s.store, repoBase)
				upstream = append(upstream, dv)
			}
		}
		for _, q := range allQueue {
			for _, dep := range q.DependsOn {
				if dep == issue {
					dv := depIssueView(q.Issue, qByIssue, s.store, repoBase)
					downstream = append(downstream, dv)
					break
				}
			}
		}
	}

	data := PipelineDetailData{
		State:         ps,
		StageOrder:    stageOrder,
		History:       history,
		Events:        events,
		IsActive:      ps.Status == "in_progress",
		SessionDot:    s.sessionDot(ps.CurrentSession),
		TmuxCmd:          tmuxCmd,
		HasLiveStream:    hasLiveStream,
		UpdatedAgo:       relTime(ps.UpdatedAt),
		IssueURL:         issueURL,
		QueueStatus:      queueStatus,
		Upstream:         upstream,
		Downstream:       downstream,
		ShouldAutoRefresh: ps.Status == "in_progress" && !hasLiveStream,
		Sidebar:          s.sidebarData(ps.Namespace),
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

	var attemptNS string
	if ps, err := s.store.Get(issue); err == nil {
		attemptNS = ps.Namespace
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
		Sidebar:      s.sidebarData(attemptNS),
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
	proj := currentProject(r)
	sidebar := s.sidebarData(proj)

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
		ns := ""
		if p, ok := pipelineByIssue[q.Issue]; ok {
			ns = p.Namespace
		} else {
			ns = s.namespaceFromConfigPath(q.ConfigPath)
		}
		if proj != "" && ns != proj {
			continue
		}
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
			Namespace:      ns,
		})
	}

	data := QueueData{Items: rows, Sidebar: sidebar}
	if err := s.queueTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- Config ----

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	proj := currentProject(r)
	sidebar := s.sidebarData(proj)

	repos := s.allRepoConfigs()

	// Filter repos by project.
	if proj != "" {
		var filtered []repoConfig
		for _, rc := range repos {
			if repoToNamespace(rc.Cfg.Pipeline.Repo) == proj {
				filtered = append(filtered, rc)
			}
		}
		repos = filtered
	}

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
			// Resolve model with same fallback chain as the engine.
			model := stage.Model
			if model == "" {
				model = rc.Cfg.Pipeline.Defaults.Model
			}
			if model == "" {
				model = "claude-opus-4-6"
			}
			view.Stages = append(view.Stages, StageConfigRow{
				ID:             stage.ID,
				Type:           stageType,
				Model:          model,
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

	data := ConfigData{Repos: views, Sidebar: sidebar}
	if err := s.configTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// depIssueView builds a DepIssueView for the given issue number.
func depIssueView(issue int, qByIssue map[int]db.QueueItem, store *pipeline.Store, repoBase string) DepIssueView {
	dv := DepIssueView{Issue: issue}
	if repoBase != "" {
		dv.GithubURL = fmt.Sprintf("%s%d", repoBase, issue)
	}
	if q, ok := qByIssue[issue]; ok {
		dv.QueueStatus = q.Status
	}
	if ps, err := store.Get(issue); err == nil {
		dv.Title = ps.Title
		dv.PipelineStatus = ps.Status
		dv.HasPipeline = true
	}
	return dv
}
