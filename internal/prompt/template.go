package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	varRe      = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
	ifOpenRe   = regexp.MustCompile(`\{\{#if\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)
	ifCloseStr = "{{/if}}"
)

// Vars is a map of variable names to values for template rendering.
type Vars map[string]string

// Render expands a template string with the given variables.
// {{variable}} is replaced with its value. Missing required variables cause an error.
// {{#if variable}}...{{/if}} blocks are included only if the variable is non-empty.
func Render(tmpl string, vars Vars) (string, error) {
	// Process conditional blocks iteratively, innermost first
	result, err := processConditionals(tmpl, vars)
	if err != nil {
		return "", err
	}

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

// processConditionals handles {{#if var}}...{{/if}} blocks, supporting nesting.
// It processes innermost blocks first by finding the last {{#if before each {{/if}}.
func processConditionals(tmpl string, vars Vars) (string, error) {
	result := tmpl
	for {
		// Find the first {{/if}}
		closeIdx := strings.Index(result, ifCloseStr)
		if closeIdx == -1 {
			break
		}

		// Find the last {{#if ...}} before this {{/if}} — that's the innermost
		prefix := result[:closeIdx]
		openLocs := ifOpenRe.FindAllStringIndex(prefix, -1)
		if openLocs == nil {
			return "", fmt.Errorf("dangling {{/if}} without matching {{#if}}")
		}

		// Take the last (innermost) opening tag
		lastOpen := openLocs[len(openLocs)-1]
		openStart := lastOpen[0]
		openEnd := lastOpen[1]

		// Extract variable name from the opening tag
		openTag := prefix[openStart:openEnd]
		m := ifOpenRe.FindStringSubmatch(openTag)
		if m == nil {
			return "", fmt.Errorf("failed to parse conditional tag: %s", openTag)
		}
		varName := m[1]

		// Extract body between opening and closing tags
		body := result[openEnd:closeIdx]
		closeEnd := closeIdx + len(ifCloseStr)

		// Evaluate: include body if variable is set and non-empty
		var replacement string
		if val, ok := vars[varName]; ok && val != "" {
			replacement = body
		}

		result = result[:openStart] + replacement + result[closeEnd:]
	}

	// Check for unclosed conditional blocks
	if ifOpenRe.MatchString(result) {
		loc := ifOpenRe.FindString(result)
		return "", fmt.Errorf("unclosed conditional block: %s", loc)
	}

	return result, nil
}

// LoadTemplate reads a template from the given path.
// It first checks for project-level overrides (relative to workdir),
// then falls back to built-in templates.
func LoadTemplate(templatePath string, workdir string) (string, error) {
	// Check project-level override first
	if workdir != "" {
		projectPath := filepath.Join(workdir, templatePath)
		// Prevent path traversal: resolved path must be within workdir
		absProject, err := filepath.Abs(projectPath)
		if err == nil {
			absWorkdir, err2 := filepath.Abs(workdir)
			if err2 == nil && !strings.HasPrefix(absProject, absWorkdir+string(filepath.Separator)) && absProject != absWorkdir {
				return "", fmt.Errorf("template path %q escapes workdir", templatePath)
			}
		}
		if data, err := os.ReadFile(projectPath); err == nil {
			return string(data), nil
		}
	}

	// Fall back to built-in templates
	dir := builtinTemplateDir()
	if dir == "" {
		return "", fmt.Errorf("template %q not found and could not determine home directory for built-in templates", templatePath)
	}
	builtinPath := filepath.Join(dir, templatePath)
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
