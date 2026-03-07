package config

import (
	"fmt"
	"net/url"
	"os"
)

// PipelineConfig is the top-level configuration structure parsed from pipeline YAML.
type PipelineConfig struct {
	Pipeline Pipeline        `yaml:"pipeline"`
	Deploy   *DeployPipeline `yaml:"deploy"`
}

// DeployPipeline defines the deploy pipeline configuration.
type DeployPipeline struct {
	Name   string  `yaml:"name"`
	Stages []Stage `yaml:"stages"`
}

// DatabaseConfig declares per-repo PostgreSQL database needs.
type DatabaseConfig struct {
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Migrate  string `yaml:"migrate"`
}

// URL returns a PostgreSQL connection string for this database config.
// The password is URL-encoded to handle special characters.
// The host is derived from the DATABASE_URL env var if set (for shared postgres),
// otherwise defaults to localhost:5432.
func (d *DatabaseConfig) URL() string {
	host := "localhost:5432"
	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		if u, err := url.Parse(envURL); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		d.User, url.PathEscape(d.Password), host, d.Name)
}

// Pipeline defines the full pipeline: metadata, defaults, checks, and stages.
type Pipeline struct {
	Name              string              `yaml:"name"`
	Repo              string              `yaml:"repo"`
	MaxFixRounds      int                 `yaml:"max_fix_rounds"`
	FreshSessionAfter int                 `yaml:"fresh_session_after"`
	Setup             []string            `yaml:"setup"`
	Database          *DatabaseConfig     `yaml:"database"`
	Env               map[string]string   `yaml:"env"`
	Defaults          StageDefaults       `yaml:"defaults"`
	DefaultChecks     []string            `yaml:"default_checks"`
	Checks            map[string]Check    `yaml:"checks"`
	Stages            []Stage             `yaml:"stages"`
	Vars              map[string]string   `yaml:"vars"`
	Notifications     NotificationsConfig `yaml:"notifications"`
}

// DiscordConfig holds Discord webhook notification settings.
type DiscordConfig struct {
	WebhookURL     string `yaml:"webhook_url"`
	ThreadPerIssue bool   `yaml:"thread_per_issue"`
}

// NotificationsConfig holds per-project notification settings.
type NotificationsConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

// StageDefaults holds default values applied to stages that don't specify their own.
type StageDefaults struct {
	Model   string `yaml:"model"`
	Timeout string `yaml:"timeout"`
	Flags   string `yaml:"flags"`
}

// Check defines a deterministic check that can be run between or after stages.
type Check struct {
	Command           string `yaml:"command"`
	Parser            string `yaml:"parser"`
	Timeout           string `yaml:"timeout"`
	FixCommand        string `yaml:"fix_command"`
	AutoFix           bool   `yaml:"auto_fix"`
	SeverityThreshold string `yaml:"severity_threshold"`
}

// Stage defines a single pipeline stage — either an agent invocation or a checks-only gate.
type Stage struct {
	ID             string            `yaml:"id"`
	Type           string            `yaml:"type"`
	PromptTemplate string            `yaml:"prompt_template"`
	Model          string            `yaml:"model"`
	ContextMode    string            `yaml:"context_mode"`
	Flags          string            `yaml:"flags"`
	GoalGate       bool              `yaml:"goal_gate"`
	SessionMode    string            `yaml:"session_mode"`
	OnFail         interface{}       `yaml:"on_fail"`
	BrowserCheck   bool              `yaml:"browser_check"`
	SkipChecks     bool              `yaml:"skip_checks"`
	ChecksAfter    []string          `yaml:"checks_after"`
	ChecksBefore   []string          `yaml:"checks_before"`
	ExtraChecks    []string          `yaml:"extra_checks"`
	Checks         []string          `yaml:"checks"`
	MergeStrategy  string            `yaml:"merge_strategy"`
	Vars           map[string]string `yaml:"vars"`
}
