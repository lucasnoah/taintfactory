package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	varRe  = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
	ifRe   = regexp.MustCompile(`(?s)\{\{#if\s+([a-zA-Z_][a-zA-Z0-9_]*)\}\}(.*?)\{\{/if\}\}`)
)

// Vars is a map of variable names to values for template rendering.
type Vars map[string]string

// Render expands a template string with the given variables.
// {{variable}} is replaced with its value. Missing required variables cause an error.
// {{#if variable}}...{{/if}} blocks are included only if the variable is non-empty.
func Render(tmpl string, vars Vars) (string, error) {
	// First pass: process conditional blocks
	result := ifRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		m := ifRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		varName := m[1]
		body := m[2]
		if val, ok := vars[varName]; ok && val != "" {
			return body
		}
		return ""
	})

	// Second pass: expand variables, collecting any missing ones
	var missing []string
	expanded := varRe.ReplaceAllStringFunc(result, func(match string) string {
		m := varRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		varName := m[1]
		if val, ok := vars[varName]; ok {
			return val
		}
		missing = append(missing, varName)
		return match // leave placeholder for error reporting
	})

	if len(missing) > 0 {
		return "", fmt.Errorf("missing template variables: %s", strings.Join(missing, ", "))
	}

	return expanded, nil
}

// LoadTemplate reads a template from the given path.
// It first checks for project-level overrides (relative to workdir),
// then falls back to built-in templates.
func LoadTemplate(templatePath string, workdir string) (string, error) {
	// Check project-level override first
	if workdir != "" {
		projectPath := filepath.Join(workdir, templatePath)
		if data, err := os.ReadFile(projectPath); err == nil {
			return string(data), nil
		}
	}

	// Fall back to built-in templates
	builtinPath := filepath.Join(builtinTemplateDir(), templatePath)
	data, err := os.ReadFile(builtinPath)
	if err != nil {
		return "", fmt.Errorf("template not found at %q (also checked %q): %w", templatePath, builtinPath, err)
	}
	return string(data), nil
}

// builtinTemplateDir returns the path to the built-in templates directory.
func builtinTemplateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".factory", "templates")
}

// InstallBuiltinTemplates writes the built-in templates to ~/.factory/templates/
// if they don't already exist.
func InstallBuiltinTemplates() error {
	dir := builtinTemplateDir()
	if dir == "" {
		return fmt.Errorf("could not determine home directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create templates dir: %w", err)
	}

	for name, content := range builtinTemplates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue // don't overwrite existing
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write template %q: %w", name, err)
		}
	}
	return nil
}
