package config

import (
	"fmt"
	"net/url"
	"os"
)

// PipelineConfig is the top-level configuration structure parsed from pipeline YAML.
type PipelineConfig struct {
	Pipeline Pipeline `yaml:"pipeline"`
}

// DatabaseConfig declares per-repo PostgreSQL database needs.
type DatabaseConfig struct {
	Name     string `yaml:"name"`
	TestName string `yaml:"test_name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Migrate  string `yaml:"migrate"`
}

// TestURLForHost returns a PostgreSQL connection string for the test database.
func (d *DatabaseConfig) TestURLForHost(host string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		d.User, url.PathEscape(d.Password), host, d.TestName)
}

// URLForHost returns a PostgreSQL connection string targeting the given host.
// The password is URL-encoded to handle special characters.
func (d *DatabaseConfig) URLForHost(host string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		d.User, url.PathEscape(d.Password), host, d.Name)
}

// URL returns a PostgreSQL connection string defaulting to localhost:5432.
func (d *DatabaseConfig) URL() string {
	return d.URLForHost("localhost:5432")
}

// DBHost extracts the host:port from the factory's DATABASE_URL env var.
// Falls back to localhost:5432 if the env var is unset or unparseable.
func DBHost() string {
	raw := os.Getenv("DATABASE_URL")
	if raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return "localhost:5432"
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
