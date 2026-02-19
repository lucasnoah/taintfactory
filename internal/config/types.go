package config

// PipelineConfig is the top-level configuration structure parsed from pipeline YAML.
type PipelineConfig struct {
	Pipeline Pipeline `yaml:"pipeline"`
}

// Pipeline defines the full pipeline: metadata, defaults, checks, and stages.
type Pipeline struct {
	Name              string            `yaml:"name"`
	Repo              string            `yaml:"repo"`
	MaxFixRounds      int               `yaml:"max_fix_rounds"`
	FreshSessionAfter int               `yaml:"fresh_session_after"`
	Defaults          StageDefaults     `yaml:"defaults"`
	DefaultChecks     []string          `yaml:"default_checks"`
	Checks            map[string]Check  `yaml:"checks"`
	Stages            []Stage           `yaml:"stages"`
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
	ID             string      `yaml:"id"`
	Type           string      `yaml:"type"`
	PromptTemplate string      `yaml:"prompt_template"`
	Model          string      `yaml:"model"`
	ContextMode    string      `yaml:"context_mode"`
	Flags          string      `yaml:"flags"`
	GoalGate       bool        `yaml:"goal_gate"`
	SessionMode    string      `yaml:"session_mode"`
	OnFail         interface{} `yaml:"on_fail"`
	BrowserCheck   bool        `yaml:"browser_check"`
	SkipChecks     bool        `yaml:"skip_checks"`
	ChecksAfter    []string    `yaml:"checks_after"`
	ChecksBefore   []string    `yaml:"checks_before"`
	ExtraChecks    []string    `yaml:"extra_checks"`
	Checks         []string    `yaml:"checks"`
}
