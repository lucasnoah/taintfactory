package config

import "fmt"

// ValidationError represents a single validation issue with a config.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// recognizedParsers is the set of valid parser names for checks.
var recognizedParsers = map[string]bool{
	"eslint":     true,
	"prettier":   true,
	"typescript": true,
	"vitest":     true,
	"npm-audit":  true,
	"generic":    true,
}

// Validate checks a PipelineConfig for structural and semantic errors.
// It returns a slice of all validation errors found (empty if valid).
func Validate(cfg *PipelineConfig) []ValidationError {
	var errs []ValidationError
	p := cfg.Pipeline

	// Required fields
	if p.Name == "" {
		errs = append(errs, ValidationError{Field: "pipeline.name", Message: "is required"})
	}
	if p.Repo == "" {
		errs = append(errs, ValidationError{Field: "pipeline.repo", Message: "is required"})
	}
	if len(p.Stages) == 0 {
		errs = append(errs, ValidationError{Field: "pipeline.stages", Message: "at least one stage is required"})
	}

	// Build set of stage IDs for reference validation
	stageIDs := make(map[string]bool)
	for i, s := range p.Stages {
		if s.ID == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.stages[%d].id", i),
				Message: "is required",
			})
			continue
		}
		if stageIDs[s.ID] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.stages[%d].id", i),
				Message: fmt.Sprintf("duplicate stage ID %q", s.ID),
			})
		}
		stageIDs[s.ID] = true
	}

	// Validate on_fail targets reference existing stage IDs
	for i, s := range p.Stages {
		validateOnFail(s, i, stageIDs, &errs)
	}

	// Validate check references in default_checks
	for _, checkName := range p.DefaultChecks {
		if _, ok := p.Checks[checkName]; !ok {
			errs = append(errs, ValidationError{
				Field:   "pipeline.default_checks",
				Message: fmt.Sprintf("references undefined check %q", checkName),
			})
		}
	}

	// Validate per-stage check references and checks_only requirements
	for i, s := range p.Stages {
		prefix := fmt.Sprintf("pipeline.stages[%d]", i)

		// checks_only stages must have explicit checks list
		if s.Type == "checks_only" && len(s.Checks) == 0 {
			errs = append(errs, ValidationError{
				Field:   prefix + ".checks",
				Message: "checks_only stage must have an explicit checks list",
			})
		}

		// Validate check name references
		for _, list := range []struct {
			name   string
			checks []string
		}{
			{"checks_after", s.ChecksAfter},
			{"checks_before", s.ChecksBefore},
			{"extra_checks", s.ExtraChecks},
			{"checks", s.Checks},
		} {
			for _, checkName := range list.checks {
				if _, ok := p.Checks[checkName]; !ok {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("%s.%s", prefix, list.name),
						Message: fmt.Sprintf("references undefined check %q", checkName),
					})
				}
			}
		}
	}

	// Validate parser names in checks
	for name, check := range p.Checks {
		if check.Parser != "" && !recognizedParsers[check.Parser] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.checks.%s.parser", name),
				Message: fmt.Sprintf("unrecognized parser %q", check.Parser),
			})
		}
	}

	return errs
}

// validateOnFail checks that on_fail values reference existing stage IDs.
func validateOnFail(s Stage, index int, stageIDs map[string]bool, errs *[]ValidationError) {
	prefix := fmt.Sprintf("pipeline.stages[%d].on_fail", index)

	switch v := s.OnFail.(type) {
	case nil:
		// No on_fail specified, that's fine
	case string:
		if v != "" && !stageIDs[v] {
			*errs = append(*errs, ValidationError{
				Field:   prefix,
				Message: fmt.Sprintf("references undefined stage %q", v),
			})
		}
	case map[string]interface{}:
		for key, val := range v {
			target, ok := val.(string)
			if !ok {
				continue
			}
			if !stageIDs[target] {
				*errs = append(*errs, ValidationError{
					Field:   fmt.Sprintf("%s.%s", prefix, key),
					Message: fmt.Sprintf("references undefined stage %q", target),
				})
			}
		}
	}
}
