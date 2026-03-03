# Discord Stage Notifications Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a `factory discord run` poller that watches pipeline_events and posts rich Discord embeds after each stage completes, with Claude-generated stage-type-specific summaries.

**Architecture:** A standalone poller command polls `pipeline_events` for new events since a persisted cursor, generates a Claude summary from the session log and git diff, builds a Discord embed, and POSTs to a per-project webhook URL configured in `pipeline.yaml`. The orchestrator is untouched.

**Tech Stack:** Go stdlib `net/http` for Discord webhooks, `gopkg.in/yaml.v3` (already in go.mod) for config, `os/exec` for `claude --print`, SQLite via existing `internal/db` package.

**Design doc:** `docs/plans/2026-03-03-discord-notifications-design.md`

---

### Task 1: Add NotificationsConfig to Pipeline Config

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write the failing test**

Add to `internal/config/config_test.go`:
```go
func TestNotificationsConfig(t *testing.T) {
    yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  notifications:
    discord:
      webhook_url: "https://discord.com/api/webhooks/123/abc"
      thread_per_issue: true
`
    cfg, err := LoadFromBytes([]byte(yaml))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if cfg.Pipeline.Notifications.Discord.WebhookURL != "https://discord.com/api/webhooks/123/abc" {
        t.Errorf("expected webhook URL, got %q", cfg.Pipeline.Notifications.Discord.WebhookURL)
    }
    if !cfg.Pipeline.Notifications.Discord.ThreadPerIssue {
        t.Error("expected ThreadPerIssue to be true")
    }
}

func TestNotificationsConfig_Empty(t *testing.T) {
    yaml := `
pipeline:
  name: test
  repo: github.com/test/test
`
    cfg, err := LoadFromBytes([]byte(yaml))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if cfg.Pipeline.Notifications.Discord.WebhookURL != "" {
        t.Error("expected empty webhook URL when not configured")
    }
}
```

**Step 2: Run tests to verify they fail**
```bash
cd ~/Documents/taintfactory && go test ./internal/config/... -run TestNotifications -v
```
Expected: FAIL — `Pipeline` has no field `Notifications`

**Step 3: Add the structs to `internal/config/types.go`**

Add after the existing `Pipeline` struct fields:
```go
type DiscordConfig struct {
    WebhookURL     string `yaml:"webhook_url"`
    ThreadPerIssue bool   `yaml:"thread_per_issue"`
}

type NotificationsConfig struct {
    Discord DiscordConfig `yaml:"discord"`
}
```

And add the field to the `Pipeline` struct:
```go
Notifications NotificationsConfig `yaml:"notifications"`
```

Also add `LoadFromBytes` to `internal/config/loader.go` if it doesn't exist:
```go
func LoadFromBytes(data []byte) (*PipelineConfig, error) {
    var cfg PipelineConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parsing config YAML: %w", err)
    }
    applyDefaults(&cfg)
    return &cfg, nil
}
```

**Step 4: Run tests to verify they pass**
```bash
go test ./internal/config/... -run TestNotifications -v
```
Expected: PASS

**Step 5: Run full test suite to verify no regressions**
```bash
go test ./...
```
Expected: all PASS

**Step 6: Commit**
```bash
git add internal/config/types.go internal/config/loader.go internal/config/config_test.go
git commit -m "feat(config): add NotificationsConfig with Discord webhook support"
```

---

### Task 2: Add GetPipelineEventsSince DB Query

**Files:**
- Modify: `internal/db/queries.go`
- Modify: `internal/db/db_test.go`

**Step 1: Write the failing test**

Add to `internal/db/db_test.go`:
```go
func TestGetPipelineEventsSince(t *testing.T) {
    d := openTestDB(t)

    // Insert 3 events
    d.LogPipelineEvent("ns/repo", 1, "stage_advanced", "implement", 1, "")
    d.LogPipelineEvent("ns/repo", 1, "stage_advanced", "review", 1, "")
    d.LogPipelineEvent("ns/repo", 1, "completed", "merge", 1, "")

    // Fetch all events since ID 0
    events, err := d.GetPipelineEventsSince(0, 100)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(events) != 3 {
        t.Fatalf("expected 3 events, got %d", len(events))
    }

    // Fetch since first event ID — should return 2
    events2, err := d.GetPipelineEventsSince(events[0].ID, 100)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(events2) != 2 {
        t.Fatalf("expected 2 events since ID %d, got %d", events[0].ID, len(events2))
    }

    // Limit test
    events3, err := d.GetPipelineEventsSince(0, 2)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(events3) != 2 {
        t.Fatalf("expected 2 events with limit 2, got %d", len(events3))
    }
}
```

**Step 2: Run test to verify it fails**
```bash
go test ./internal/db/... -run TestGetPipelineEventsSince -v
```
Expected: FAIL — `GetPipelineEventsSince` undefined

**Step 3: Add the query to `internal/db/queries.go`**

Add after `GetPipelineHistory`:
```go
// GetPipelineEventsSince returns pipeline events with ID > lastID, up to limit rows,
// ordered by ID ascending (oldest first for sequential processing).
func (d *DB) GetPipelineEventsSince(lastID int, limit int) ([]PipelineEvent, error) {
    rows, err := d.conn.Query(`
        SELECT id, namespace, issue, event, COALESCE(stage,''), COALESCE(attempt,0), COALESCE(detail,''), timestamp
        FROM pipeline_events
        WHERE id > ?
        ORDER BY id ASC
        LIMIT ?`, lastID, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var events []PipelineEvent
    for rows.Next() {
        var e PipelineEvent
        if err := rows.Scan(&e.ID, &e.Namespace, &e.Issue, &e.Event, &e.Stage, &e.Attempt, &e.Detail, &e.Timestamp); err != nil {
            return nil, err
        }
        events = append(events, e)
    }
    return events, rows.Err()
}
```

**Step 4: Run test to verify it passes**
```bash
go test ./internal/db/... -run TestGetPipelineEventsSince -v
```
Expected: PASS

**Step 5: Commit**
```bash
git add internal/db/queries.go internal/db/db_test.go
git commit -m "feat(db): add GetPipelineEventsSince for Discord poller cursor"
```

---

### Task 3: Cursor State (internal/discord/cursor.go)

**Files:**
- Create: `internal/discord/cursor.go`
- Create: `internal/discord/cursor_test.go`

**Step 1: Write the failing test**

Create `internal/discord/cursor_test.go`:
```go
package discord_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/lucasnoah/taintfactory/internal/discord"
)

func TestCursor_LoadSave(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "discord_cursor.json")

    c := discord.NewCursor(path)

    // Fresh cursor starts at 0
    if c.LastEventID() != 0 {
        t.Errorf("expected 0, got %d", c.LastEventID())
    }

    // Save and reload
    c.Advance(42)
    if err := c.Save(); err != nil {
        t.Fatalf("save failed: %v", err)
    }

    c2 := discord.NewCursor(path)
    if c2.LastEventID() != 42 {
        t.Errorf("expected 42 after reload, got %d", c2.LastEventID())
    }
}

func TestCursor_ThreadID(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "discord_cursor.json")

    c := discord.NewCursor(path)
    c.SetThreadID("ns/repo", 99, "thread-abc-123")
    c.Save()

    c2 := discord.NewCursor(path)
    id := c2.ThreadID("ns/repo", 99)
    if id != "thread-abc-123" {
        t.Errorf("expected thread-abc-123, got %q", id)
    }

    // Missing returns empty string
    if c2.ThreadID("ns/repo", 100) != "" {
        t.Error("expected empty string for unknown issue")
    }
}
```

**Step 2: Run test to verify it fails**
```bash
go test ./internal/discord/... -v
```
Expected: FAIL — package not found

**Step 3: Create `internal/discord/cursor.go`**
```go
package discord

import (
    "encoding/json"
    "fmt"
    "os"
)

// Cursor persists the last-seen pipeline_events ID and thread IDs for issues.
type Cursor struct {
    path    string
    state   cursorState
}

type cursorState struct {
    LastEventID int               `json:"last_event_id"`
    ThreadIDs   map[string]string `json:"thread_ids"` // "ns/repo:issue" -> discord thread ID
}

func NewCursor(path string) *Cursor {
    c := &Cursor{path: path, state: cursorState{ThreadIDs: make(map[string]string)}}
    _ = c.load() // ignore error — fresh cursor if file missing
    return c
}

func (c *Cursor) LastEventID() int { return c.state.LastEventID }

func (c *Cursor) Advance(id int) { c.state.LastEventID = id }

func (c *Cursor) ThreadID(namespace string, issue int) string {
    return c.state.ThreadIDs[fmt.Sprintf("%s:%d", namespace, issue)]
}

func (c *Cursor) SetThreadID(namespace string, issue int, threadID string) {
    c.state.ThreadIDs[fmt.Sprintf("%s:%d", namespace, issue)] = threadID
}

func (c *Cursor) Save() error {
    data, err := json.MarshalIndent(c.state, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(c.path, data, 0644)
}

func (c *Cursor) load() error {
    data, err := os.ReadFile(c.path)
    if err != nil {
        return err
    }
    return json.Unmarshal(data, &c.state)
}
```

**Step 4: Run tests to verify they pass**
```bash
go test ./internal/discord/... -v
```
Expected: PASS

**Step 5: Commit**
```bash
git add internal/discord/cursor.go internal/discord/cursor_test.go
git commit -m "feat(discord): cursor state — persist last event ID and thread IDs"
```

---

### Task 4: Discord Embed Builder (internal/discord/embed.go)

**Files:**
- Create: `internal/discord/embed.go`
- Create: `internal/discord/embed_test.go`

**Step 1: Write the failing tests**

Create `internal/discord/embed_test.go`:
```go
package discord_test

import (
    "encoding/json"
    "strings"
    "testing"

    "github.com/lucasnoah/taintfactory/internal/discord"
)

func TestBuildAgentEmbed_Success(t *testing.T) {
    params := discord.EmbedParams{
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

    payload := discord.BuildAgentEmbed(params)

    data, err := json.Marshal(payload)
    if err != nil {
        t.Fatalf("marshal failed: %v", err)
    }
    s := string(data)

    if !strings.Contains(s, "#285") {
        t.Error("expected issue number in embed")
    }
    if !strings.Contains(s, "implement") {
        t.Error("expected stage name in embed")
    }
    // Green color for success
    if !strings.Contains(s, "3066993") { // 0x2ECC71 = 3066993
        t.Error("expected green color for success")
    }
}

func TestBuildAgentEmbed_FailColor(t *testing.T) {
    params := discord.EmbedParams{
        Issue: 285, Namespace: "ns/repo", Stage: "review",
        Outcome: "fail", Duration: "5m", FixRounds: 2,
    }
    payload := discord.BuildAgentEmbed(params)
    data, _ := json.Marshal(payload)
    s := string(data)
    // Red color for failure
    if !strings.Contains(s, "15158332") { // 0xE74C3C = 15158332
        t.Error("expected red color for failure")
    }
}

func TestBuildCompletionEmbed(t *testing.T) {
    params := discord.CompletionEmbedParams{
        Issue:         285,
        Namespace:     "mbrucker/deathcookies",
        Outcome:       "completed",
        TotalDuration: "49m3s",
        IssueTitle:    "P&L DB layer: schema + sqlc queries",
        StageChain:    "implement → review → qa → verify → merge → contract-check",
    }
    payload := discord.BuildCompletionEmbed(params)
    data, _ := json.Marshal(payload)
    s := string(data)
    if !strings.Contains(s, "49m3s") {
        t.Error("expected total duration in embed")
    }
    if !strings.Contains(s, "P&L DB layer") {
        t.Error("expected issue title in embed footer")
    }
}
```

**Step 2: Run test to verify it fails**
```bash
go test ./internal/discord/... -run TestBuildAgentEmbed -v
```
Expected: FAIL

**Step 3: Create `internal/discord/embed.go`**
```go
package discord

import "fmt"

const (
    colorGreen  = 3066993  // 0x2ECC71
    colorYellow = 16312092 // 0xF8C419
    colorRed    = 15158332 // 0xE74C3C
)

// WebhookPayload is the top-level Discord webhook JSON body.
type WebhookPayload struct {
    Embeds []Embed `json:"embeds"`
}

type Embed struct {
    Title       string       `json:"title"`
    Color       int          `json:"color"`
    Fields      []EmbedField `json:"fields"`
    Footer      *EmbedFooter `json:"footer,omitempty"`
}

type EmbedField struct {
    Name   string `json:"name"`
    Value  string `json:"value"`
    Inline bool   `json:"inline"`
}

type EmbedFooter struct {
    Text string `json:"text"`
}

// EmbedParams holds data for an agent stage notification.
type EmbedParams struct {
    Issue         int
    Namespace     string
    Stage         string
    Outcome       string // "success" or "fail"
    Duration      string
    FixRounds     int
    StageIndex    int
    TotalStages   int
    Summary       string
    Changes       string
    OpenQuestions string
}

// CompletionEmbedParams holds data for a pipeline completion/failure notification.
type CompletionEmbedParams struct {
    Issue         int
    Namespace     string
    Outcome       string // "completed" or "failed"
    TotalDuration string
    IssueTitle    string
    StageChain    string
}

func BuildAgentEmbed(p EmbedParams) WebhookPayload {
    statusIcon := "✅"
    color := colorGreen
    if p.Outcome == "fail" {
        statusIcon = "❌"
        color = colorRed
    } else if p.FixRounds > 0 {
        color = colorYellow
    }

    title := fmt.Sprintf("#%d %s %s   %s", p.Issue, p.Stage, statusIcon, p.Namespace)

    fields := []EmbedField{
        {Name: "Duration", Value: p.Duration, Inline: true},
        {Name: "Fix Rounds", Value: fmt.Sprintf("%d", p.FixRounds), Inline: true},
    }

    if p.Summary != "" {
        fields = append(fields, EmbedField{Name: "Summary", Value: p.Summary})
    }
    if p.Changes != "" {
        fields = append(fields, EmbedField{Name: "Changes", Value: p.Changes})
    }

    questions := p.OpenQuestions
    if questions == "" {
        questions = "—"
    }
    fields = append(fields, EmbedField{Name: "Open Questions", Value: questions})

    footer := &EmbedFooter{Text: fmt.Sprintf("stage %d of %d", p.StageIndex, p.TotalStages)}

    return WebhookPayload{Embeds: []Embed{{Title: title, Color: color, Fields: fields, Footer: footer}}}
}

func BuildCompletionEmbed(p CompletionEmbedParams) WebhookPayload {
    statusIcon := "✅"
    color := colorGreen
    if p.Outcome == "failed" {
        statusIcon = "❌"
        color = colorRed
    }

    title := fmt.Sprintf("#%d %s %s   %s", p.Issue, p.Outcome, statusIcon, p.Namespace)

    fields := []EmbedField{
        {Name: "Total Duration", Value: p.TotalDuration, Inline: true},
        {Name: "Stages", Value: p.StageChain},
    }

    return WebhookPayload{Embeds: []Embed{{
        Title:  title,
        Color:  color,
        Fields: fields,
        Footer: &EmbedFooter{Text: p.IssueTitle},
    }}}
}

func BuildStaticEmbed(issue int, namespace, stage, message string, success bool) WebhookPayload {
    icon := "✅"
    color := colorGreen
    if !success {
        icon = "❌"
        color = colorRed
    }
    title := fmt.Sprintf("#%d %s %s   %s", issue, stage, icon, namespace)
    return WebhookPayload{Embeds: []Embed{{
        Title:  title,
        Color:  color,
        Fields: []EmbedField{{Name: "Status", Value: message}},
    }}}
}
```

**Step 4: Run tests to verify they pass**
```bash
go test ./internal/discord/... -run TestBuild -v
```
Expected: PASS

**Step 5: Commit**
```bash
git add internal/discord/embed.go internal/discord/embed_test.go
git commit -m "feat(discord): embed builder — agent stage and completion payloads"
```

---

### Task 5: Webhook Poster (internal/discord/webhook.go)

**Files:**
- Create: `internal/discord/webhook.go`
- Create: `internal/discord/webhook_test.go`

**Step 1: Write the failing test**

Create `internal/discord/webhook_test.go`:
```go
package discord_test

import (
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/lucasnoah/taintfactory/internal/discord"
)

func TestPost_SendsJSON(t *testing.T) {
    var received WebhookPayload
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        json.Unmarshal(body, &received)
        if r.Header.Get("Content-Type") != "application/json" {
            t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
        }
        w.WriteHeader(http.StatusNoContent) // Discord returns 204
    }))
    defer srv.Close()

    payload := discord.WebhookPayload{
        Embeds: []discord.Embed{{Title: "test embed", Color: 3066993}},
    }

    if err := discord.Post(srv.URL, payload, ""); err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if received.Embeds[0].Title != "test embed" {
        t.Errorf("expected 'test embed', got %q", received.Embeds[0].Title)
    }
}

func TestPost_WithThreadID(t *testing.T) {
    var capturedURL string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedURL = r.URL.String()
        w.WriteHeader(http.StatusNoContent)
    }))
    defer srv.Close()

    payload := discord.WebhookPayload{Embeds: []discord.Embed{{Title: "t"}}}
    discord.Post(srv.URL, payload, "thread-123")

    if capturedURL != "/?thread_id=thread-123" {
        t.Errorf("expected thread_id in URL, got %s", capturedURL)
    }
}

func TestPost_ErrorOnBadStatus(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusTooManyRequests)
    }))
    defer srv.Close()

    payload := discord.WebhookPayload{Embeds: []discord.Embed{{Title: "t"}}}
    if err := discord.Post(srv.URL, payload, ""); err == nil {
        t.Error("expected error on non-2xx status")
    }
}

type WebhookPayload = discord.WebhookPayload
```

**Step 2: Run test to verify it fails**
```bash
go test ./internal/discord/... -run TestPost -v
```
Expected: FAIL

**Step 3: Create `internal/discord/webhook.go`**
```go
package discord

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
)

// Post sends a WebhookPayload to a Discord webhook URL.
// If threadID is non-empty, posts to that thread via ?thread_id= query param.
func Post(webhookURL string, payload WebhookPayload, threadID string) error {
    data, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("marshal payload: %w", err)
    }

    url := webhookURL
    if threadID != "" {
        url = webhookURL + "?thread_id=" + threadID
    }

    resp, err := http.Post(url, "application/json", bytes.NewReader(data))
    if err != nil {
        return fmt.Errorf("posting to Discord: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("Discord returned %d", resp.StatusCode)
    }
    return nil
}
```

**Step 4: Run tests to verify they pass**
```bash
go test ./internal/discord/... -run TestPost -v
```
Expected: PASS

**Step 5: Commit**
```bash
git add internal/discord/webhook.go internal/discord/webhook_test.go
git commit -m "feat(discord): webhook poster with thread_id support"
```

---

### Task 6: Claude Summarizer (internal/discord/summarize.go)

**Files:**
- Create: `internal/discord/summarize.go`
- Create: `internal/discord/summarize_test.go`

**Step 1: Write the failing test**

Create `internal/discord/summarize_test.go`:
```go
package discord_test

import (
    "testing"

    "github.com/lucasnoah/taintfactory/internal/discord"
)

func TestStageSummaryPrompt_Implement(t *testing.T) {
    prompt := discord.BuildSummaryPrompt("implement", "session log content here", "diff content here")
    if !contains(prompt, "implemented") {
        t.Error("implement prompt should ask about what was implemented")
    }
    if !contains(prompt, "session log content here") {
        t.Error("prompt should include session log")
    }
}

func TestStageSummaryPrompt_Review(t *testing.T) {
    prompt := discord.BuildSummaryPrompt("review", "log", "diff")
    if !contains(prompt, "flagged") {
        t.Error("review prompt should ask about what was flagged")
    }
}

func TestStageSummaryPrompt_ContractCheck(t *testing.T) {
    prompt := discord.BuildSummaryPrompt("contract-check", "log", "diff")
    if !contains(prompt, "contract") {
        t.Error("contract-check prompt should mention contracts")
    }
}

func TestStageSummaryPrompt_Verify(t *testing.T) {
    prompt := discord.BuildSummaryPrompt("verify", "log", "diff")
    if prompt != "" {
        t.Error("verify stage should return empty prompt (static message)")
    }
}

func contains(s, sub string) bool {
    return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
    for i := 0; i <= len(s)-len(sub); i++ {
        if s[i:i+len(sub)] == sub {
            return true
        }
    }
    return false
}
```

**Step 2: Run test to verify it fails**
```bash
go test ./internal/discord/... -run TestStageSummaryPrompt -v
```
Expected: FAIL

**Step 3: Create `internal/discord/summarize.go`**
```go
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
        if after, ok := cutPrefix(line, "Summary: "); ok {
            result.Summary = strings.TrimSpace(after)
        } else if after, ok := cutPrefix(line, "Changes: "); ok {
            result.Changes = strings.TrimSpace(after)
        } else if after, ok := cutPrefix(line, "Open Questions: "); ok {
            result.OpenQuestions = strings.TrimSpace(after)
        }
    }
    return result
}

func cutPrefix(s, prefix string) (string, bool) {
    if strings.HasPrefix(s, prefix) {
        return s[len(prefix):], true
    }
    return "", false
}

func truncate(s string, max int) string {
    if len(s) <= max {
        return s
    }
    return "..." + s[len(s)-max:]
}
```

**Step 4: Run tests to verify they pass**
```bash
go test ./internal/discord/... -run TestStageSummaryPrompt -v
```
Expected: PASS

**Step 5: Commit**
```bash
git add internal/discord/summarize.go internal/discord/summarize_test.go
git commit -m "feat(discord): stage-type-specific Claude summary prompts"
```

---

### Task 7: Poller Command (internal/cli/discord.go)

**Files:**
- Create: `internal/cli/discord.go`
- Modify: `internal/cli/root.go` (add `rootCmd.AddCommand(discordCmd)`)

**Step 1: Review the pattern in `internal/cli/queue.go`**

Before writing, read the first 50 lines of `internal/cli/queue.go` to confirm the cobra registration pattern matches what we use below.

**Step 2: Create `internal/cli/discord.go`**
```go
package cli

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/lucasnoah/taintfactory/internal/config"
    "github.com/lucasnoah/taintfactory/internal/db"
    "github.com/lucasnoah/taintfactory/internal/discord"
    "github.com/lucasnoah/taintfactory/internal/pipeline"
    "github.com/spf13/cobra"
)

var discordCmd = &cobra.Command{
    Use:   "discord",
    Short: "Discord notifications for pipeline stages",
}

var discordRunCmd = &cobra.Command{
    Use:   "run",
    Short: "Poll pipeline events and post Discord stage notifications",
    RunE:  runDiscordPoller,
}

func runDiscordPoller(cmd *cobra.Command, args []string) error {
    interval, _ := cmd.Flags().GetDuration("interval")

    dbPath, err := db.DefaultDBPath()
    if err != nil {
        return err
    }
    d, err := db.Open(dbPath)
    if err != nil {
        return err
    }
    defer d.Close()

    store, err := pipeline.DefaultStore()
    if err != nil {
        return err
    }

    home, _ := os.UserHomeDir()
    cursor := discord.NewCursor(filepath.Join(home, ".factory", "discord_cursor.json"))

    fmt.Printf("Discord poller started (interval: %s, cursor: %d)\n", interval, cursor.LastEventID())

    for {
        if err := pollOnce(d, store, cursor); err != nil {
            fmt.Fprintf(os.Stderr, "poll error: %v\n", err)
        }
        time.Sleep(interval)
    }
}

func pollOnce(d *db.DB, store *pipeline.Store, cursor *discord.Cursor) error {
    events, err := d.GetPipelineEventsSince(cursor.LastEventID(), 50)
    if err != nil {
        return err
    }

    for _, evt := range events {
        if err := handleEvent(d, store, cursor, evt); err != nil {
            fmt.Fprintf(os.Stderr, "event %d error: %v\n", evt.ID, err)
        }
        cursor.Advance(evt.ID)
        _ = cursor.Save()
    }
    return nil
}

// relevantEvents are the stage transitions we notify on.
var relevantEvents = map[string]bool{
    "stage_advanced": true,
    "completed":      true,
    "failed":         true,
    "escalated":      true,
}

func handleEvent(d *db.DB, store *pipeline.Store, cursor *discord.Cursor, evt db.PipelineEvent) error {
    if !relevantEvents[evt.Event] {
        return nil
    }

    // Load pipeline config to get webhook URL.
    qi, err := d.GetQueueItem(evt.Namespace, evt.Issue)
    if err != nil || qi.ConfigPath == "" {
        return nil // no config, skip silently
    }
    cfg, err := config.Load(qi.ConfigPath)
    if err != nil {
        return nil
    }
    webhookURL := cfg.Pipeline.Notifications.Discord.WebhookURL
    if webhookURL == "" {
        return nil // project not configured for Discord
    }

    threadPerIssue := cfg.Pipeline.Notifications.Discord.ThreadPerIssue
    threadID := ""
    if threadPerIssue {
        threadID = cursor.ThreadID(evt.Namespace, evt.Issue)
    }

    // Load pipeline state for durations, stage history, title.
    ps, err := store.GetForNamespace(evt.Namespace, evt.Issue)
    if err != nil {
        return fmt.Errorf("load pipeline state: %w", err)
    }

    var payload discord.WebhookPayload

    switch evt.Event {
    case "completed", "failed":
        payload = buildCompletionPayload(ps, evt)
    case "stage_advanced", "escalated":
        payload = buildStagePayload(store, ps, evt)
    }

    if err := discord.Post(webhookURL, payload, threadID); err != nil {
        return fmt.Errorf("post to Discord: %w", err)
    }
    return nil
}

func buildStagePayload(store *pipeline.Store, ps *pipeline.PipelineState, evt db.PipelineEvent) discord.WebhookPayload {
    // The stage that just completed is evt.Stage (the "from" stage on advancement).
    completedStage := evt.Stage

    // Find stage history entry for the completed stage.
    var entry pipeline.StageHistoryEntry
    for _, h := range ps.StageHistory {
        if h.Stage == completedStage {
            entry = h
        }
    }

    // Static stages — no Claude summary.
    if completedStage == "verify" || completedStage == "merge" {
        msg := "All checks passed."
        if completedStage == "merge" {
            msg = "Merged to main."
        }
        return discord.BuildStaticEmbed(ps.Issue, ps.Namespace, completedStage, msg, entry.Outcome == "success")
    }

    // Agent stages — generate Claude summary.
    sessionLog, _ := store.GetSessionLog(ps.Issue, completedStage, entry.Attempt)
    gitDiff := getGitDiff(ps.Worktree)
    prompt := discord.BuildSummaryPrompt(completedStage, sessionLog, gitDiff)
    summary := discord.GenerateSummary(prompt)

    stageIndex, totalStages := stagePosition(ps, completedStage)

    return discord.BuildAgentEmbed(discord.EmbedParams{
        Issue:         ps.Issue,
        Namespace:     ps.Namespace,
        Stage:         completedStage,
        Outcome:       entry.Outcome,
        Duration:      entry.Duration,
        FixRounds:     entry.FixRounds,
        StageIndex:    stageIndex,
        TotalStages:   totalStages,
        Summary:       summary.Summary,
        Changes:       summary.Changes,
        OpenQuestions: summary.OpenQuestions,
    })
}

func buildCompletionPayload(ps *pipeline.PipelineState, evt db.PipelineEvent) discord.WebhookPayload {
    totalDuration := totalPipelineDuration(ps)
    chain := stageChain(ps)
    return discord.BuildCompletionEmbed(discord.CompletionEmbedParams{
        Issue:         ps.Issue,
        Namespace:     ps.Namespace,
        Outcome:       evt.Event,
        TotalDuration: totalDuration,
        IssueTitle:    ps.Title,
        StageChain:    chain,
    })
}

func getGitDiff(worktree string) string {
    if worktree == "" {
        return ""
    }
    out, err := runGit(worktree, "diff", "HEAD~1", "HEAD", "--stat")
    if err != nil {
        return ""
    }
    return out
}

func runGit(dir string, args ...string) (string, error) {
    // Thin wrapper so tests can substitute it later if needed.
    import_exec := func() ([]byte, error) {
        return nil, nil
    }
    _ = import_exec
    // Real implementation:
    cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
    out, err := cmd.Output()
    return string(out), err
}

func stagePosition(ps *pipeline.PipelineState, stage string) (index, total int) {
    // Count from stage history — index is position of this stage, total is len(history).
    total = len(ps.StageHistory)
    for i, h := range ps.StageHistory {
        if h.Stage == stage {
            return i + 1, total
        }
    }
    return total, total
}

func totalPipelineDuration(ps *pipeline.PipelineState) string {
    if ps.CreatedAt == "" || len(ps.StageHistory) == 0 {
        return "unknown"
    }
    // Sum all stage durations.
    var total time.Duration
    for _, h := range ps.StageHistory {
        d, err := time.ParseDuration(h.Duration)
        if err == nil {
            total += d
        }
    }
    return total.Round(time.Second).String()
}

func stageChain(ps *pipeline.PipelineState) string {
    names := make([]string, 0, len(ps.StageHistory))
    for _, h := range ps.StageHistory {
        names = append(names, h.Stage)
    }
    return strings.Join(names, " → ")
}

func init() {
    discordRunCmd.Flags().Duration("interval", 15*time.Second, "How often to poll for new events")
    discordCmd.AddCommand(discordRunCmd)
}
```

**Note:** `runGit` uses `os/exec` — add `"os/exec"` to imports.

**Step 3: Register discordCmd in `internal/cli/root.go`**

Find the `init()` block in `root.go` and add:
```go
rootCmd.AddCommand(discordCmd)
```

**Step 4: Add `GetQueueItem` helper to `internal/db/queries.go`**

The poller needs to look up `config_path` by namespace+issue. Add:
```go
// GetQueueItem returns the queue entry for a given namespace and issue number.
func (d *DB) GetQueueItem(namespace string, issue int) (*QueueItem, error) {
    row := d.conn.QueryRow(`
        SELECT id, namespace, issue, status, position, feature_intent, depends_on, config_path, added_at,
               COALESCE(started_at,''), COALESCE(finished_at,'')
        FROM issue_queue WHERE namespace = ? AND issue = ?`, namespace, issue)

    var q QueueItem
    var depsJSON string
    err := row.Scan(&q.ID, &q.Namespace, &q.Issue, &q.Status, &q.Position,
        &q.FeatureIntent, &depsJSON, &q.ConfigPath, &q.AddedAt, &q.StartedAt, &q.FinishedAt)
    if err != nil {
        return nil, err
    }
    _ = json.Unmarshal([]byte(depsJSON), &q.DependsOn)
    return &q, nil
}
```

**Step 5: Build to verify it compiles**
```bash
cd ~/Documents/taintfactory && go build ./...
```
Expected: no errors

**Step 6: Smoke test the command help**
```bash
/tmp/factory discord --help
/tmp/factory discord run --help
```
Expected: help text shows with `--interval` flag

**Step 7: Commit**
```bash
git add internal/cli/discord.go internal/cli/root.go internal/db/queries.go
git commit -m "feat(discord): poller command — factory discord run"
```

---

### Task 8: Wire Discord into pipeline.yaml for deathcookies

**Files:**
- Modify: `~/Documents/deathcookies/pipeline.yaml`

**Step 1: Add notifications block to deathcookies pipeline.yaml**

Add under the `pipeline:` key (after `fresh_session_after`):
```yaml
  notifications:
    discord:
      webhook_url: "https://discord.com/api/webhooks/REPLACE_WITH_REAL_URL"
      thread_per_issue: false
```

**Step 2: Validate config parses cleanly**
```bash
cd ~/Documents/deathcookies && /tmp/factory config validate --config pipeline.yaml
```
Expected: no errors

**Step 3: Run a dry poll to verify events are found**
```bash
unset CLAUDECODE && /tmp/factory discord run --interval 999999s &
sleep 3 && kill %1
```
Expected: prints "Discord poller started" and processes recent events (or "no new events")

---

### Task 9: Full Integration Test

**Step 1: Run the full test suite**
```bash
cd ~/Documents/taintfactory && go test ./...
```
Expected: all PASS

**Step 2: Build the final binary**
```bash
go build -o /tmp/factory ./cmd/factory/
```

**Step 3: Verify discord subcommand is registered**
```bash
/tmp/factory --help | grep discord
```
Expected: `discord` appears in the command list

**Step 4: Final commit**
```bash
git add .
git commit -m "feat: Discord stage notifications — factory discord run

Standalone poller posts rich Discord embeds after each pipeline stage.
Per-project webhook config in pipeline.yaml. Claude-generated summaries
with stage-type-specific prompts (implement/review/qa/contract-check).
Thread-per-issue optional. Cursor persisted at ~/.factory/discord_cursor.json.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```
