package cli

import (
	"fmt"
	"os"
	"os/exec"
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
	cursor, err := discord.LoadCursor(filepath.Join(home, ".factory", "discord_cursor.json"))
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}

	fmt.Printf("Discord poller started (interval: %s, cursor: %d)\n", interval, cursor.LastEventID)

	for {
		if err := pollOnce(d, store, cursor); err != nil {
			fmt.Fprintf(os.Stderr, "poll error: %v\n", err)
		}
		time.Sleep(interval)
	}
}

// relevantEvents are the pipeline transitions that trigger Discord notifications.
var relevantEvents = map[string]bool{
	"stage_advanced": true,
	"completed":      true,
	"failed":         true,
	"escalated":      true,
}

func pollOnce(d *db.DB, store *pipeline.Store, cursor *discord.Cursor) error {
	events, err := d.GetPipelineEventsSince(cursor.LastEventID, 50)
	if err != nil {
		return err
	}

	for _, evt := range events {
		if err := handleDiscordEvent(d, store, evt); err != nil {
			fmt.Fprintf(os.Stderr, "event %d error: %v\n", evt.ID, err)
		}
		cursor.LastEventID = evt.ID
		_ = cursor.Save()
	}
	return nil
}

func handleDiscordEvent(d *db.DB, store *pipeline.Store, evt db.PipelineEvent) error {
	if !relevantEvents[evt.Event] {
		return nil
	}

	// Load queue item to get config_path.
	qi, err := d.GetQueueItem(evt.Issue)
	if err != nil || qi == nil || qi.ConfigPath == "" {
		return nil // no config, skip silently
	}
	cfg, err := config.Load(qi.ConfigPath)
	if err != nil {
		return nil // can't load config, skip silently
	}
	webhookURL := cfg.Pipeline.Notifications.Discord.WebhookURL
	if webhookURL == "" {
		return nil // project not configured for Discord
	}

	// Load pipeline state for stage history, namespace, etc.
	ps, err := store.Get(evt.Issue)
	if err != nil {
		return fmt.Errorf("load pipeline state: %w", err)
	}

	var payload discord.WebhookPayload

	switch evt.Event {
	case "completed", "failed":
		payload = buildDiscordCompletionPayload(ps, evt)
	case "stage_advanced", "escalated":
		payload = buildDiscordStagePayload(store, ps, evt)
	default:
		return nil
	}

	// thread_per_issue is not yet implemented (requires cursor extension for thread ID storage).
	threadID := ""

	if err := discord.Post(webhookURL, payload, threadID); err != nil {
		return fmt.Errorf("post to Discord: %w", err)
	}
	return nil
}

func buildDiscordStagePayload(store *pipeline.Store, ps *pipeline.PipelineState, evt db.PipelineEvent) discord.WebhookPayload {
	// For stage_advanced: evt.Stage is the NEXT stage; completed stage is in
	// evt.Detail as "from=<stage>". Extract the completed stage from there.
	completedStage := evt.Stage
	if after, ok := strings.CutPrefix(evt.Detail, "from="); ok {
		completedStage = after
	}

	// Find the history entry for the completed stage.
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
	gitDiff := discordGetGitDiff(ps.Worktree)
	prompt := discord.BuildSummaryPrompt(completedStage, sessionLog, gitDiff)
	summary := discord.GenerateSummary(prompt)

	stageIndex, totalStages := discordStagePosition(ps, completedStage)

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

func buildDiscordCompletionPayload(ps *pipeline.PipelineState, evt db.PipelineEvent) discord.WebhookPayload {
	return discord.BuildCompletionEmbed(discord.CompletionEmbedParams{
		Issue:         ps.Issue,
		Namespace:     ps.Namespace,
		Outcome:       evt.Event,
		TotalDuration: discordTotalDuration(ps),
		IssueTitle:    ps.Title,
		StageChain:    discordStageChain(ps),
	})
}

func discordGetGitDiff(worktree string) string {
	if worktree == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", worktree, "diff", "HEAD~1", "HEAD", "--stat")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func discordStagePosition(ps *pipeline.PipelineState, stage string) (index, total int) {
	total = len(ps.StageHistory)
	for i, h := range ps.StageHistory {
		if h.Stage == stage {
			return i + 1, total
		}
	}
	return total, total
}

func discordTotalDuration(ps *pipeline.PipelineState) string {
	var total time.Duration
	for _, h := range ps.StageHistory {
		d, err := time.ParseDuration(h.Duration)
		if err == nil {
			total += d
		}
	}
	if total == 0 {
		return "unknown"
	}
	return total.Round(time.Second).String()
}

func discordStageChain(ps *pipeline.PipelineState) string {
	names := make([]string, 0, len(ps.StageHistory))
	for _, h := range ps.StageHistory {
		names = append(names, h.Stage)
	}
	return strings.Join(names, " → ")
}

// discordPollTick runs one pass of the Discord poller. Called from orchestrator
// check-in so notifications are sent on the same cadence as pipeline advances.
// Errors are non-fatal — a failed notification should not block the orchestrator.
func discordPollTick() error {
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
	cursor, err := discord.LoadCursor(filepath.Join(home, ".factory", "discord_cursor.json"))
	if err != nil {
		return err
	}

	return pollOnce(d, store, cursor)
}

func init() {
	discordRunCmd.Flags().Duration("interval", 15*time.Second, "How often to poll for new events")
	discordCmd.AddCommand(discordRunCmd)
}
