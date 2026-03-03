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

// Embed is a single Discord embed object.
type Embed struct {
	Title  string       `json:"title"`
	Color  int          `json:"color"`
	Fields []EmbedField `json:"fields"`
	Footer *EmbedFooter `json:"footer,omitempty"`
}

// EmbedField is a name/value pair within an embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// EmbedFooter is the footer of a Discord embed.
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

// BuildAgentEmbed constructs a Discord webhook payload for an agent stage result.
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

// BuildCompletionEmbed constructs a Discord webhook payload for a pipeline completion or failure.
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

// BuildStaticEmbed constructs a simple status embed for non-agent stages.
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
