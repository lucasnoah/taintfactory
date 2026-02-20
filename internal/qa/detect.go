package qa

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/github"
)

// DetectResult holds the browser detection analysis.
type DetectResult struct {
	BrowserTestNeeded  bool     `json:"browser_test_needed"`
	Reasons            []string `json:"reasons"`
	AffectedRoutes     []string `json:"affected_routes"`
	AffectedComponents []string `json:"affected_components"`
}

// DetectOpts configures browser test detection.
type DetectOpts struct {
	Issue        *github.Issue
	FilesChanged []string // list of changed file paths
	ForceFlag    bool     // stage config browser_check flag
}

// browser-related issue labels
var browserLabels = map[string]bool{
	"frontend": true,
	"ui":       true,
	"browser":  true,
	"web":      true,
	"css":      true,
	"ux":       true,
}

// browser-related file extensions
var browserExtensions = map[string]bool{
	".tsx":  true,
	".jsx":  true,
	".html": true,
	".css":  true,
	".scss": true,
	".less": true,
	".vue":  true,
	".svelte": true,
}

// browser-related directory patterns
var browserDirs = []string{
	"components/",
	"pages/",
	"routes/",
	"views/",
	"layouts/",
	"styles/",
	"public/",
	"static/",
	"templates/",
	"app/",
	"src/ui/",
}

// keywords in issue body suggesting browser testing
var browserKeywords = regexp.MustCompile(
	`(?i)\b(button|form|modal|dialog|navigation|page|ui|dropdown|` +
		`input field|checkbox|radio|tooltip|popover|sidebar|` +
		`header|footer|menu|tab|accordion|carousel|slider|` +
		`login|signup|dashboard|landing page|responsive|` +
		`click|hover|scroll|drag|drop|resize|animate|` +
		`browser|viewport|mobile|desktop|css|style|layout)\b`,
)

// route extraction pattern: /path/like/this
var routePattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9])(\/[a-z][a-z0-9\-\/]*[a-z0-9])`)

// component extraction from file names (PascalCase filenames in component dirs)
var componentNamePattern = regexp.MustCompile(`([A-Z][a-zA-Z0-9]+)`)

// DetectBrowserTest analyzes whether an issue needs browser testing.
func DetectBrowserTest(opts DetectOpts) *DetectResult {
	result := &DetectResult{
		Reasons:            []string{},
		AffectedRoutes:     []string{},
		AffectedComponents: []string{},
	}

	// Check explicit force flag
	if opts.ForceFlag {
		result.BrowserTestNeeded = true
		result.Reasons = append(result.Reasons, "Stage config has browser_check: true")
	}

	// Check issue labels
	if opts.Issue != nil {
		for _, label := range opts.Issue.Labels {
			if browserLabels[strings.ToLower(label.Name)] {
				result.BrowserTestNeeded = true
				result.Reasons = append(result.Reasons, "Issue has browser-related label: "+label.Name)
				break
			}
		}

		// Check issue body keywords
		if opts.Issue.Body != "" {
			matches := browserKeywords.FindAllString(opts.Issue.Body, -1)
			if len(matches) > 0 {
				seen := make(map[string]bool)
				var unique []string
				for _, m := range matches {
					lower := strings.ToLower(m)
					if !seen[lower] {
						seen[lower] = true
						unique = append(unique, lower)
					}
				}
				if len(unique) >= 2 {
					result.BrowserTestNeeded = true
					result.Reasons = append(result.Reasons, "Issue body mentions browser-related terms: "+strings.Join(unique[:min(3, len(unique))], ", "))
				}
			}
		}

		// Extract routes from issue body
		routeMatches := routePattern.FindAllStringSubmatch(opts.Issue.Body, -1)
		seen := make(map[string]bool)
		for _, m := range routeMatches {
			route := m[1]
			if !seen[route] && !isCommonNonRoute(route) {
				seen[route] = true
				result.AffectedRoutes = append(result.AffectedRoutes, route)
			}
		}
	}

	// Check files changed
	if len(opts.FilesChanged) > 0 {
		browserFiles := analyzeBrowserFiles(opts.FilesChanged)
		if browserFiles.hasBrowserExtension {
			result.BrowserTestNeeded = true
			result.Reasons = append(result.Reasons, "Files changed include browser-related extensions: "+strings.Join(browserFiles.extensions, ", "))
		}
		if browserFiles.hasBrowserDir {
			result.BrowserTestNeeded = true
			result.Reasons = append(result.Reasons, "Files changed include browser-related directories: "+strings.Join(browserFiles.dirs, ", "))
		}
		result.AffectedComponents = append(result.AffectedComponents, browserFiles.components...)

		// Extract routes from file paths (pages/ or routes/ directories)
		for _, f := range opts.FilesChanged {
			if route := extractRouteFromPath(f); route != "" {
				result.AffectedRoutes = append(result.AffectedRoutes, route)
			}
		}
	}

	// Deduplicate routes
	result.AffectedRoutes = dedupe(result.AffectedRoutes)
	result.AffectedComponents = dedupe(result.AffectedComponents)

	return result
}

type browserFileAnalysis struct {
	hasBrowserExtension bool
	hasBrowserDir       bool
	extensions          []string
	dirs                []string
	components          []string
}

func analyzeBrowserFiles(files []string) browserFileAnalysis {
	result := browserFileAnalysis{}
	seenExts := make(map[string]bool)
	seenDirs := make(map[string]bool)

	for _, f := range files {
		ext := filepath.Ext(f)
		if browserExtensions[ext] && !seenExts[ext] {
			result.hasBrowserExtension = true
			seenExts[ext] = true
			result.extensions = append(result.extensions, ext)
		}

		for _, dir := range browserDirs {
			if strings.Contains(f, dir) && !seenDirs[dir] {
				result.hasBrowserDir = true
				seenDirs[dir] = true
				result.dirs = append(result.dirs, strings.TrimSuffix(dir, "/"))
			}
		}

		// Extract component names from PascalCase filenames in component-like directories
		base := filepath.Base(f)
		nameOnly := strings.TrimSuffix(base, filepath.Ext(base))
		if componentNamePattern.MatchString(nameOnly) && isBrowserFile(f) {
			result.components = append(result.components, nameOnly)
		}
	}

	return result
}

func isBrowserFile(path string) bool {
	ext := filepath.Ext(path)
	return browserExtensions[ext]
}

// extractRouteFromPath extracts a route from a pages/ or routes/ file path.
func extractRouteFromPath(path string) string {
	for _, prefix := range []string{"pages/", "routes/", "app/"} {
		idx := strings.Index(path, prefix)
		if idx >= 0 {
			rest := path[idx+len(prefix):]
			// Remove extension and index files
			rest = strings.TrimSuffix(rest, filepath.Ext(rest))
			rest = strings.TrimSuffix(rest, "/index")
			rest = strings.TrimSuffix(rest, "index")
			if rest == "" {
				return "/"
			}
			return "/" + rest
		}
	}
	return ""
}

// isCommonNonRoute filters out paths that look like routes but aren't.
func isCommonNonRoute(path string) bool {
	nonRoutes := []string{"/bin", "/etc", "/tmp", "/var", "/usr", "/dev", "/opt", "/home"}
	for _, nr := range nonRoutes {
		if strings.HasPrefix(path, nr) {
			return true
		}
	}
	return false
}

func dedupe(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
