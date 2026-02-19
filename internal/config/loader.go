package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a pipeline configuration from the given YAML file path.
// After parsing, it applies defaults to stages that don't specify their own values.
func Load(path string) (*PipelineConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg PipelineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadDefault searches for a pipeline config in standard locations and loads the
// first one found. Search order: ./pipeline.yaml, ~/.factory/config.yaml
func LoadDefault() (*PipelineConfig, error) {
	candidates := []string{"pipeline.yaml"}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(home, ".factory", "config.yaml"))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return Load(path)
		}
	}

	return nil, fmt.Errorf("no pipeline config found (searched: %v)", candidates)
}

// applyDefaults merges pipeline-level defaults into stages that don't set their own values,
// and resolves default_checks for stages that don't specify checks_after or skip_checks.
func applyDefaults(cfg *PipelineConfig) {
	p := &cfg.Pipeline

	for i := range p.Stages {
		s := &p.Stages[i]

		// Apply default model
		if s.Model == "" && p.Defaults.Model != "" {
			s.Model = p.Defaults.Model
		}

		// Apply default flags
		if s.Flags == "" && p.Defaults.Flags != "" {
			s.Flags = p.Defaults.Flags
		}

		// Resolve default_checks: stages without explicit checks_after and without skip_checks
		// get the pipeline's default_checks.
		if len(s.ChecksAfter) == 0 && !s.SkipChecks && s.Type != "checks_only" {
			s.ChecksAfter = p.DefaultChecks
		}
	}
}
