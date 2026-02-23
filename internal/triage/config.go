package triage

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// TriageConfig is the top-level structure parsed from triage.yaml.
type TriageConfig struct {
	Triage TriageMeta    `yaml:"triage"`
	Stages []TriageStage `yaml:"stages"`
}

// TriageMeta holds repository-level metadata.
type TriageMeta struct {
	Name string `yaml:"name"`
	Repo string `yaml:"repo"` // e.g. "owner/repo" — used as the state directory slug
}

// TriageStage defines one stage in the triage pipeline.
type TriageStage struct {
	ID             string            `yaml:"id"`
	PromptTemplate string            `yaml:"prompt_template"` // optional override path relative to repo root
	Timeout        string            `yaml:"timeout"`
	Outcomes       map[string]string `yaml:"outcomes"` // e.g. {"stale": "done", "clean": "already_implemented"}
}

// Load reads and parses a triage config from the given YAML file path.
func Load(path string) (*TriageConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg TriageConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadDefault loads triage.yaml from the given directory (typically the repo root).
func LoadDefault(repoRoot string) (*TriageConfig, error) {
	path := filepath.Join(repoRoot, "triage.yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no triage.yaml found in %s", repoRoot)
	}
	return Load(path)
}

// StageByID returns the stage with the given ID, or nil if not found.
func (cfg *TriageConfig) StageByID(id string) *TriageStage {
	for i := range cfg.Stages {
		if cfg.Stages[i].ID == id {
			return &cfg.Stages[i]
		}
	}
	return nil
}

// NextStageID returns the ID of the stage after the given one, or "" if it's the last.
func (cfg *TriageConfig) NextStageID(currentID string) string {
	for i, s := range cfg.Stages {
		if s.ID == currentID {
			if i+1 < len(cfg.Stages) {
				return cfg.Stages[i+1].ID
			}
			return ""
		}
	}
	return ""
}

// applyDefaults sets default timeout of "15m" for stages that don't specify one.
func applyDefaults(cfg *TriageConfig) {
	for i := range cfg.Stages {
		if cfg.Stages[i].Timeout == "" {
			cfg.Stages[i].Timeout = "15m"
		}
	}
}
